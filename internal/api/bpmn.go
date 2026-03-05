package api

import (
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ── Operaton proxy helpers ──────────────────────────────────────────────────

var bpmnClient = &http.Client{Timeout: 30 * time.Second}

func operatonURL(d *Deps) string {
	if d != nil && d.Cfg != nil && d.Cfg.OperatonURL != "" {
		return d.Cfg.OperatonURL
	}
	return "http://polyon-operaton:8080"
}

// bpmnProxy forwards a request to Operaton engine-rest and writes response back.
func bpmnProxy(d *Deps, method, path string, body io.Reader, w http.ResponseWriter, r *http.Request) {
	// Forward query string
	fullPath := path
	if q := r.URL.RawQuery; q != "" {
		fullPath += "?" + q
	}

	req, err := http.NewRequest(method, operatonURL(d)+fullPath, body)
	if err != nil {
		httputil.RespondError(w, 500, "internal_error", "build request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := bpmnClient.Do(req)
	if err != nil {
		httputil.RespondError(w, 502, "gateway_error", "operaton unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// RegisterBPMN registers Operaton BPMN engine management routes.
func RegisterBPMN(r chi.Router, d *Deps) {
	r.Route("/engines/bpmn", func(r chi.Router) {
		// Process definitions
		r.Get("/processes", bpmnListProcesses(d))
		r.Get("/processes/{id}", bpmnGetProcess(d))

		// Running instances
		r.Get("/instances", bpmnListInstances(d))
		r.Get("/instances/{id}", bpmnGetInstance(d))
		r.Delete("/instances/{id}", bpmnDeleteInstance(d))

		// Tasks
		r.Get("/tasks", bpmnListTasks(d))
		r.Get("/tasks/{id}", bpmnGetTask(d))

		// History
		r.Get("/history", bpmnListHistory(d))

		// Deployments
		r.Get("/deployments", bpmnListDeployments(d))
		r.Delete("/deployments/{id}", bpmnDeleteDeployment(d))

		// Incidents
		r.Get("/incidents", bpmnListIncidents(d))

		// Engine info
		r.Get("/engine", bpmnListEngines(d))

		// Stats
		r.Get("/stats", bpmnGetStats(d))

		// ── Phase 2: Detail + Operations ──

		// Process definition XML (BPMN diagram)
		r.Get("/processes/{id}/xml", bpmnGetProcessXML(d))
		r.Get("/processes/key/{key}/xml", bpmnGetProcessXMLByKey(d))

		// Start process instance
		r.Post("/processes/key/{key}/start", bpmnStartProcess(d))

		// Instance variables
		r.Get("/instances/{id}/variables", bpmnGetInstanceVariables(d))
		r.Put("/instances/{id}/variables/{name}", bpmnPutInstanceVariable(d))

		// Instance activity
		r.Get("/instances/{id}/activity-instances", bpmnGetActivityInstances(d))

		// Instance modification (cancel/retry)
		r.Put("/instances/{id}/suspended", bpmnSuspendInstance(d))

		// Task operations
		r.Post("/tasks/{id}/claim", bpmnClaimTask(d))
		r.Post("/tasks/{id}/unclaim", bpmnUnclaimTask(d))
		r.Post("/tasks/{id}/complete", bpmnCompleteTask(d))
		r.Post("/tasks/{id}/delegate", bpmnDelegateTask(d))
		r.Post("/tasks/{id}/assign", bpmnAssignTask(d))
		r.Get("/tasks/{id}/form-variables", bpmnGetTaskFormVars(d))
		r.Get("/tasks/{id}/identity-links", bpmnGetTaskIdentityLinks(d))

		// Deployment create (multipart upload)
		r.Post("/deployments", bpmnCreateDeployment(d))
		r.Get("/deployments/{id}/resources", bpmnGetDeploymentResources(d))
		r.Get("/deployments/{id}/resources/{resId}/data", bpmnGetResourceData(d))

		// Incidents
		r.Put("/incidents/{id}/retries", bpmnRetryIncident(d))

		// Jobs
		r.Get("/jobs", bpmnListJobs(d))
		r.Put("/jobs/{id}/retries", bpmnSetJobRetries(d))
		r.Put("/jobs/{id}/suspended", bpmnSuspendJob(d))

		// History details
		r.Get("/history/{id}/activity-instances", bpmnHistoryActivityInstances(d))
		r.Get("/history/{id}/variable-instances", bpmnHistoryVariableInstances(d))

		// Decision definitions (DMN)
		r.Get("/decisions", bpmnListDecisions(d))
		r.Get("/decisions/history", bpmnListDecisionHistory(d))
		r.Get("/decisions/{id}", bpmnGetDecision(d))
		r.Get("/decisions/{id}/xml", bpmnGetDecisionXML(d))
	})
}

// ── Process Definitions ──

func bpmnListProcesses(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/process-definition", nil, w, r)
	}
}

func bpmnGetProcess(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/process-definition/"+id, nil, w, r)
	}
}

// ── Process Instances ──

func bpmnListInstances(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/process-instance", nil, w, r)
	}
}

func bpmnGetInstance(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/process-instance/"+id, nil, w, r)
	}
}

func bpmnDeleteInstance(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "DELETE", "/engine-rest/process-instance/"+id, r.Body, w, r)
	}
}

// ── Tasks ──

func bpmnListTasks(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/task", nil, w, r)
	}
}

func bpmnGetTask(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/task/"+id, nil, w, r)
	}
}

// ── History ──

func bpmnListHistory(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/history/process-instance", nil, w, r)
	}
}

// ── Deployments ──

func bpmnListDeployments(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/deployment", nil, w, r)
	}
}

func bpmnDeleteDeployment(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		// cascade=true to also delete running instances
		bpmnProxy(d, "DELETE", "/engine-rest/deployment/"+id+"?cascade=true", nil, w, r)
	}
}

// ── Incidents ──

func bpmnListIncidents(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/incident", nil, w, r)
	}
}

// ── Engine info ──

func bpmnListEngines(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/engine", nil, w, r)
	}
}

// ── Stats (process definition statistics) ──

func bpmnGetStats(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/process-definition/statistics", nil, w, r)
	}
}

// ── Process Definition XML ──

func bpmnGetProcessXML(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/process-definition/"+id+"/xml", nil, w, r)
	}
}

func bpmnGetProcessXMLByKey(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "key")
		bpmnProxy(d, "GET", "/engine-rest/process-definition/key/"+key+"/xml", nil, w, r)
	}
}

// ── Start Process ──

func bpmnStartProcess(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "key")
		bpmnProxy(d, "POST", "/engine-rest/process-definition/key/"+key+"/start", r.Body, w, r)
	}
}

// ── Instance Variables ──

func bpmnGetInstanceVariables(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/process-instance/"+id+"/variables", nil, w, r)
	}
}

func bpmnPutInstanceVariable(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")
		bpmnProxy(d, "PUT", "/engine-rest/process-instance/"+id+"/variables/"+name, r.Body, w, r)
	}
}

// ── Activity Instances ──

func bpmnGetActivityInstances(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/process-instance/"+id+"/activity-instances", nil, w, r)
	}
}

// ── Suspend/Resume Instance ──

func bpmnSuspendInstance(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "PUT", "/engine-rest/process-instance/"+id+"/suspended", r.Body, w, r)
	}
}

// ── Task Operations ──

func bpmnClaimTask(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "POST", "/engine-rest/task/"+id+"/claim", r.Body, w, r)
	}
}

func bpmnUnclaimTask(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "POST", "/engine-rest/task/"+id+"/unclaim", r.Body, w, r)
	}
}

func bpmnCompleteTask(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "POST", "/engine-rest/task/"+id+"/complete", r.Body, w, r)
	}
}

func bpmnDelegateTask(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "POST", "/engine-rest/task/"+id+"/delegate", r.Body, w, r)
	}
}

func bpmnAssignTask(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "POST", "/engine-rest/task/"+id+"/assignee", r.Body, w, r)
	}
}

func bpmnGetTaskFormVars(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/task/"+id+"/form-variables", nil, w, r)
	}
}

func bpmnGetTaskIdentityLinks(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/task/"+id+"/identity-links", nil, w, r)
	}
}

// ── Deployment Create (multipart) ──

func bpmnCreateDeployment(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Forward multipart as-is
		req, err := http.NewRequest("POST", operatonURL(d)+"/engine-rest/deployment/create", r.Body)
		if err != nil {
			httputil.RespondError(w, 500, "internal_error", err.Error())
			return
		}
		req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		resp, err := bpmnClient.Do(req)
		if err != nil {
			httputil.RespondError(w, 502, "gateway_error", "operaton unreachable: "+err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

func bpmnGetDeploymentResources(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/deployment/"+id+"/resources", nil, w, r)
	}
}

func bpmnGetResourceData(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		resId := chi.URLParam(r, "resId")
		bpmnProxy(d, "GET", "/engine-rest/deployment/"+id+"/resources/"+resId+"/data", nil, w, r)
	}
}

// ── Incident retry ──

func bpmnRetryIncident(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "PUT", "/engine-rest/external-task/"+id+"/retries", r.Body, w, r)
	}
}

// ── Jobs ──

func bpmnListJobs(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/job", nil, w, r)
	}
}

func bpmnSetJobRetries(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "PUT", "/engine-rest/job/"+id+"/retries", r.Body, w, r)
	}
}

func bpmnSuspendJob(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "PUT", "/engine-rest/job/"+id+"/suspended", r.Body, w, r)
	}
}

// ── Decision Definitions (DMN) ──

func bpmnListDecisions(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/decision-definition", nil, w, r)
	}
}

func bpmnGetDecision(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/decision-definition/"+id, nil, w, r)
	}
}

func bpmnGetDecisionXML(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/decision-definition/"+id+"/xml", nil, w, r)
	}
}

func bpmnListDecisionHistory(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bpmnProxy(d, "GET", "/engine-rest/history/decision-instance", nil, w, r)
	}
}

// ── History Detail ──

func bpmnHistoryActivityInstances(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/history/activity-instance?processInstanceId="+id, nil, w, r)
	}
}

func bpmnHistoryVariableInstances(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		bpmnProxy(d, "GET", "/engine-rest/history/variable-instance?processInstanceId="+id, nil, w, r)
	}
}

// RegisterEngineRestProxy provides a Camunda Modeler compatible /engine-rest/* proxy.
// This allows external tools to deploy BPMN/DMN through Traefik → Core → Operaton
// without exposing Operaton's port directly (5th principle: single gateway).
func RegisterEngineRestProxy(r chi.Router, d *Deps) {
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		// Strip /engine-rest prefix and forward to Operaton
		path := "/engine-rest" + chi.URLParam(r, "*")
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
			// Forward body + content-type as-is (multipart for deployment)
			req, err := http.NewRequest(r.Method, operatonURL(d)+path, r.Body)
			if err != nil {
				httputil.RespondError(w, 500, "internal_error", err.Error())
				return
			}
			req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
			req.Header.Set("Accept", r.Header.Get("Accept"))
			resp, err := bpmnClient.Do(req)
			if err != nil {
				httputil.RespondError(w, 502, "gateway_error", "operaton unreachable: "+err.Error())
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		} else {
			bpmnProxy(d, r.Method, path, nil, w, r)
		}
	})
}
