package prc

import (
	"fmt"
	"regexp"
	"strings"
)

// templatePattern matches {{ claims.TYPE.KEY }}
var templatePattern = regexp.MustCompile(`\{\{\s*claims\.([a-zA-Z]+)\.([a-zA-Z]+)\s*\}\}`)

// ResolveEnvTemplate replaces {{ claims.TYPE.KEY }} placeholders with actual
// credentials from provisioning results.
//
// Example:
//
//	"{{ claims.database.url }}" → "postgres://mod_drive:xxx@polyon-db:5432/polyon_drive"
//	"{{ claims.objectStorage.endpoint }}" → "http://polyon-rustfs:9000"
func ResolveEnvTemplate(envMap map[string]string, creds map[string]Credentials) (map[string]string, error) {
	result := make(map[string]string, len(envMap))
	var missing []string

	for key, tmpl := range envMap {
		resolved := templatePattern.ReplaceAllStringFunc(tmpl, func(match string) string {
			parts := templatePattern.FindStringSubmatch(match)
			if len(parts) != 3 {
				missing = append(missing, match)
				return match
			}
			claimType := parts[1]
			credKey := parts[2]

			if c, ok := creds[claimType]; ok {
				if v, ok := c[credKey]; ok {
					return v
				}
			}
			missing = append(missing, fmt.Sprintf("%s.%s", claimType, credKey))
			return match
		})
		result[key] = resolved
	}

	if len(missing) > 0 {
		return result, fmt.Errorf("unresolved templates: %s", strings.Join(missing, ", "))
	}
	return result, nil
}

// MergeStaticEnv merges static env values (no template) with resolved templates.
// Static values override templates if there's a conflict.
func MergeStaticEnv(resolved, static map[string]string) map[string]string {
	merged := make(map[string]string, len(resolved)+len(static))
	for k, v := range resolved {
		merged[k] = v
	}
	for k, v := range static {
		merged[k] = v
	}
	return merged
}
