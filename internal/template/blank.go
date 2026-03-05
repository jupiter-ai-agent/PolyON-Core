// Package template provides embedded Next.js template files.
package template

import "encoding/base64"

// BlankTemplateFiles returns a map of filePath → base64-encoded content
// for the minimal blank starter template.
func BlankTemplateFiles() map[string]string {
	files := map[string]string{
		"package.json":        blankPackageJSON,
		"next.config.js":      blankNextConfigJS,
		".gitignore":          blankGitignore,
		"pages/_app.js":       blankPagesApp,
		"pages/index.js":      blankPagesIndex,
		"styles/globals.css":  blankStylesGlobal,
	}

	result := make(map[string]string, len(files))
	for path, content := range files {
		result[path] = base64.StdEncoding.EncodeToString([]byte(content))
	}
	return result
}

const blankPackageJSON = `{
  "name": "polyon-blank",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "lint": "next lint"
  },
  "dependencies": {
    "next": "14.2.3",
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "eslint": "^8",
    "eslint-config-next": "14.2.3"
  }
}
`

const blankNextConfigJS = `/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  trailingSlash: true,
  images: {
    unoptimized: true,
  },
  env: {
    NEXT_PUBLIC_STRAPI_URL: process.env.NEXT_PUBLIC_STRAPI_URL || '',
  },
}

module.exports = nextConfig
`

const blankGitignore = `node_modules/
.next/
out/
.env*.local
.DS_Store
`

const blankPagesApp = `import '../styles/globals.css'

export default function App({ Component, pageProps }) {
  return <Component {...pageProps} />
}
`

const blankPagesIndex = `export default function Home() {
  return (
    <main>
      <h1>Hello, HELIOS!</h1>
      <p>여기서 시작하세요. 이 파일을 수정해 원하는 페이지를 만들어보세요.</p>
    </main>
  )
}
`

const blankStylesGlobal = `/* PolyON Blank Template — globals.css */
@import url('https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:wght@300;400;500;600;700&display=swap');

*, *::before, *::after {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

body {
  font-family: 'IBM Plex Sans', -apple-system, BlinkMacSystemFont, sans-serif;
  font-size: 16px;
  line-height: 1.6;
  color: #161616;
  background: #ffffff;
  -webkit-font-smoothing: antialiased;
}

a { color: #0f62fe; text-decoration: none; }
a:hover { text-decoration: underline; }
img { max-width: 100%; display: block; }

main {
  max-width: 1200px;
  margin: 0 auto;
  padding: 4rem 1.5rem;
}

h1 { font-size: 2rem; font-weight: 700; margin-bottom: 1rem; }
h2 { font-size: 1.5rem; font-weight: 600; margin-bottom: .75rem; }
p { margin-bottom: 1rem; color: #525252; }
`
