package api

import (
	"bufio"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ── RegisterMailHistory ────────────────────────────────────────────────────────

func RegisterMailHistory(r chi.Router, d *Deps) {
	r.Get("/mail/history", mailHistoryHandler(d))
}

// ── Response types ────────────────────────────────────────────────────────────

type MailHistoryItem struct {
	Timestamp string   `json:"timestamp"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	Size      int64    `json:"size"`
	MessageID string   `json:"messageId"`
	QueueID   string   `json:"queueId"`
	Status    string   `json:"status"`
	Event     string   `json:"event"`
}

// ── Regexps ───────────────────────────────────────────────────────────────────

// message-ingest lines:
// 2026-02-20T13:21:17Z INFO ... (message-ingest.spam) ... queueId = 292..., from = "x", to = ["y"], size = 1568, ... messageId = "..."
var reIngest = regexp.MustCompile(
	`^(\S+Z)\s+INFO\s+.*\((message-ingest\.\w+)\).*?queueId\s*=\s*(\d+).*?from\s*=\s*"([^"]*)".*?to\s*=\s*\[([^\]]*)\].*?size\s*=\s*(\d+).*?messageId\s*=\s*"([^"]*)"`,
)

// delivery lines: queue.queue-message / delivery.attempt-start / delivery.completed / delivery.dsn-*
var reDelivery = regexp.MustCompile(
	`^(\S+Z)\s+(\w+)\s+.*\(([\w\.\-]+)\).*?queueId\s*=\s*(\d+).*?from\s*=\s*"([^"]*)".*?to\s*=\s*\[([^\]]*)\].*?size\s*=\s*(\d+)`,
)

// ── Log reader ────────────────────────────────────────────────────────────────

func readContainerLog(d *Deps, date string) string {
	path := "/opt/stalwart/logs/stalwart.log." + date
	out, err := d.Docker.ExecCommand("polyon-mail", "cat", path)
	if err != nil {
		return ""
	}
	return out
}

func logDates() []string {
	now := time.Now().UTC()
	return []string{
		now.Format("2006-01-02"),
		now.AddDate(0, 0, -1).Format("2006-01-02"),
		now.AddDate(0, 0, -2).Format("2006-01-02"),
	}
}

// ── Parsers ───────────────────────────────────────────────────────────────────

func parseToList(raw string) []string {
	// raw = `"a@x", "b@y"` or `"a@x"`
	var result []string
	// strip surrounding quotes from each item
	for _, part := range strings.Split(raw, ",") {
		addr := strings.Trim(strings.TrimSpace(part), `"`)
		if addr != "" {
			result = append(result, addr)
		}
	}
	return result
}

func parseIngestLines(text string) []MailHistoryItem {
	seen := map[string]bool{}
	var items []MailHistoryItem
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "message-ingest.") {
			continue
		}
		m := reIngest.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// m: [full, timestamp, event, queueId, from, to, size, messageId]
		key := m[3] + "|" + m[7] // queueId + messageId
		if seen[key] {
			continue
		}
		seen[key] = true

		sz, _ := strconv.ParseInt(m[6], 10, 64)
		status := "ham"
		if strings.HasSuffix(m[2], ".spam") {
			status = "spam"
		}
		items = append(items, MailHistoryItem{
			Timestamp: m[1],
			Event:     m[2],
			QueueID:   m[3],
			From:      m[4],
			To:        parseToList(m[5]),
			Size:      sz,
			MessageID: m[7],
			Status:    status,
		})
	}
	return items
}

func parseDeliveryLines(text string) []MailHistoryItem {
	// We use delivery.completed as the canonical status, fall back to delivery.attempt-start
	type entry struct {
		item   MailHistoryItem
		status string // completed, failed, pending
	}
	byQueue := map[string]*entry{}

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		// Only care about delivery.* and queue.queue-message
		if !strings.Contains(line, "delivery.") && !strings.Contains(line, "queue.queue-message") {
			continue
		}
		m := reDelivery.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// m: [full, timestamp, level, event, queueId, from, to, size]
		ts := m[1]
		event := m[3]
		qid := m[4]
		from := m[5]
		toList := parseToList(m[6])
		sz, _ := strconv.ParseInt(m[7], 10, 64)

		e, ok := byQueue[qid]
		if !ok {
			e = &entry{
				item: MailHistoryItem{
					Timestamp: ts,
					QueueID:   qid,
					From:      from,
					To:        toList,
					Size:      sz,
					Event:     event,
				},
				status: "pending",
			}
			byQueue[qid] = e
		}

		switch {
		case strings.Contains(event, "completed"):
			e.status = "completed"
			e.item.Status = "completed"
		case strings.Contains(event, "dsn-success"):
			e.status = "completed"
			e.item.Status = "completed"
		case strings.Contains(event, "dsn-fail"), strings.Contains(event, "dsn-perm-fail"):
			if e.status != "completed" {
				e.status = "failed"
				e.item.Status = "failed"
			}
		case strings.Contains(event, "queue-message"):
			if e.status == "pending" {
				e.item.Timestamp = ts
				e.item.Event = event
			}
		}
	}

	var items []MailHistoryItem
	for _, e := range byQueue {
		if e.item.Status == "" {
			e.item.Status = e.status
		}
		items = append(items, e.item)
	}
	return items
}

// ── Handler ───────────────────────────────────────────────────────────────────

func mailHistoryHandler(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		histType := r.URL.Query().Get("type") // "received" | "delivery"
		limitStr := r.URL.Query().Get("limit")
		pageStr := r.URL.Query().Get("page")
		search := strings.ToLower(r.URL.Query().Get("search"))

		limit := 100
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 500 {
			limit = v
		}
		page := 1
		if v, err := strconv.Atoi(pageStr); err == nil && v > 1 {
			page = v
		}

		// Collect logs from last 3 days
		var combined strings.Builder
		for _, date := range logDates() {
			combined.WriteString(readContainerLog(d, date))
			combined.WriteByte('\n')
		}
		text := combined.String()

		var items []MailHistoryItem
		if histType == "delivery" {
			items = parseDeliveryLines(text)
		} else {
			items = parseIngestLines(text)
		}

		// Sort by timestamp DESC
		sort.Slice(items, func(i, j int) bool {
			return items[i].Timestamp > items[j].Timestamp
		})

		// Search filter
		if search != "" {
			var filtered []MailHistoryItem
			for _, it := range items {
				if strings.Contains(strings.ToLower(it.From), search) ||
					strings.Contains(strings.ToLower(strings.Join(it.To, ",")), search) ||
					strings.Contains(strings.ToLower(it.MessageID), search) ||
					strings.Contains(strings.ToLower(it.QueueID), search) {
					filtered = append(filtered, it)
				}
			}
			items = filtered
		}

		total := len(items)

		// Paginate
		start := (page - 1) * limit
		end := start + limit
		if start >= total {
			items = []MailHistoryItem{}
		} else {
			if end > total {
				end = total
			}
			items = items[start:end]
		}

		if items == nil {
			items = []MailHistoryItem{}
		}

		httputil.RespondOK(w, map[string]interface{}{
			"items": items,
			"total": total,
			"page":  page,
			"limit": limit,
		})
	}
}
