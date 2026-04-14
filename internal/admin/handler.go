package admin

import (
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"agentmem/internal/auth"
	"agentmem/internal/engine"
	models "agentmem/internal/models"
	"agentmem/internal/repository/accountrepo"
	"agentmem/internal/repository/agentrepo"
	"agentmem/internal/repository/memoryrepo"
	"agentmem/internal/repository/userrepo"
)

//go:embed templates/*.html
var templateFS embed.FS

type Handler struct {
	accountRepo accountrepo.Repository
	agentRepo   agentrepo.Repository
	memoryRepo  memoryrepo.Repository
	userRepo    userrepo.Repository
	engine      *engine.MemoryEngine
}

func NewHandler(
	accountRepo accountrepo.Repository,
	agentRepo agentrepo.Repository,
	memoryRepo memoryrepo.Repository,
	userRepo userrepo.Repository,
	eng *engine.MemoryEngine,
) *Handler {
	return &Handler{
		accountRepo: accountRepo,
		agentRepo:   agentRepo,
		memoryRepo:  memoryRepo,
		userRepo:    userRepo,
		engine:      eng,
	}
}

// tmpl builds a fresh template set for each render to avoid block name conflicts
// when multiple page templates define the same "content" block.
func tmpl(page string) *template.Template {
	return template.Must(template.ParseFS(templateFS, "templates/layout.html", "templates/"+page))
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux, adminMw func(http.Handler) http.Handler) {
	mux.HandleFunc("GET /admin/login", h.loginPage)

	protected := http.NewServeMux()
	protected.HandleFunc("GET /admin/", h.dashboard)
	protected.HandleFunc("GET /admin/accounts", h.listAccounts)
	protected.HandleFunc("POST /admin/accounts", h.createAccount)
	protected.HandleFunc("DELETE /admin/accounts/{id}", h.deleteAccount)
	protected.HandleFunc("GET /admin/agents", h.listAgents)
	protected.HandleFunc("POST /admin/agents", h.createAgent)
	protected.HandleFunc("PUT /admin/agents/{id}", h.updateAgent)
	protected.HandleFunc("DELETE /admin/agents/{id}", h.deleteAgent)
	protected.HandleFunc("GET /admin/threads", h.listThreads)
	protected.HandleFunc("GET /admin/threads/{id}", h.threadDetail)
	protected.HandleFunc("DELETE /admin/threads/{id}", h.deleteThread)
	protected.HandleFunc("GET /admin/users", h.listUsers)
	protected.HandleFunc("PUT /admin/users/{id}/role", h.updateUserRole)
	protected.HandleFunc("DELETE /admin/users/{id}", h.deleteUser)
	protected.HandleFunc("GET /admin/playground", h.playgroundPage)
	protected.HandleFunc("GET /admin/playground/agents", h.playgroundAgents)
	protected.HandleFunc("GET /admin/playground/threads", h.playgroundThreads)
	protected.HandleFunc("POST /admin/playground/contextual", h.playgroundContextual)
	protected.HandleFunc("POST /admin/playground/factual", h.playgroundFactual)
	protected.HandleFunc("POST /admin/playground/recall", h.playgroundRecall)

	mux.Handle("/admin/", adminMw(protected))
}

type pageData struct {
	Title string
	Nav   string
	User  *models.User
	Flash string

	// page-specific
	Accounts      []models.Account
	Agents        []models.Agent
	Threads       []models.Thread
	Thread        *models.Thread
	Events        []models.Event
	Facts         []models.Fact
	Users         []models.User
	FilterAccount string
	FilterAgent   string

	// playground
	PlaygroundResult *PlaygroundResult
}

func (h *Handler) page(r *http.Request, title, nav string) pageData {
	return pageData{
		Title: title,
		Nav:   nav,
		User:  auth.UserFromContext(r.Context()),
	}
}

func (h *Handler) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl(name).ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "template", name, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// --- Pages ---

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t := template.Must(template.ParseFS(templateFS, "templates/login.html"))
	if err := t.ExecuteTemplate(w, "login.html", nil); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusTemporaryRedirect)
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accountRepo.ListAllAccounts(r.Context())
	if err != nil {
		slog.Error("list accounts", "error", err)
		http.Error(w, "failed to list accounts", http.StatusInternalServerError)
		return
	}
	data := h.page(r, "Accounts", "accounts")
	data.Accounts = accounts
	h.render(w, "accounts.html", data)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	acct, err := h.accountRepo.CreateAccount(r.Context(), name)
	if err != nil {
		slog.Error("create account", "error", err)
		http.Error(w, "failed to create account", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		h.renderAccountRow(w, acct)
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.accountRepo.DeleteAccountByID(r.Context(), id); err != nil {
		slog.Error("delete account", "error", err)
		http.Error(w, "failed to delete account", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/accounts", http.StatusSeeOther)
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	agents, err := h.agentRepo.ListAllAgents(r.Context(), accountID)
	if err != nil {
		slog.Error("list agents", "error", err)
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}
	accounts, _ := h.accountRepo.ListAllAccounts(r.Context())
	data := h.page(r, "Agents", "agents")
	data.Agents = agents
	data.Accounts = accounts
	data.FilterAccount = accountID
	h.render(w, "agents.html", data)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if accountID == "" || name == "" {
		http.Error(w, "account_id and name are required", http.StatusBadRequest)
		return
	}
	agent, err := h.agentRepo.CreateAgent(r.Context(), accountID, name)
	if err != nil {
		slog.Error("create agent", "error", err)
		http.Error(w, "failed to create agent", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		h.renderAgentRow(w, agent)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if err := h.agentRepo.UpdateAgent(r.Context(), accountID, id, name); err != nil {
		slog.Error("update agent", "error", err)
		http.Error(w, "failed to update agent", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if _, err := h.agentRepo.DeleteAgentByID(r.Context(), accountID, id); err != nil {
		slog.Error("delete agent", "error", err)
		http.Error(w, "failed to delete agent", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}

func (h *Handler) listThreads(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	var agentIDPtr *string
	if agentID != "" {
		agentIDPtr = &agentID
	}
	threads, err := h.agentRepo.ListAllThreads(r.Context(), "", agentIDPtr)
	if err != nil {
		slog.Error("list threads", "error", err)
		http.Error(w, "failed to list threads", http.StatusInternalServerError)
		return
	}
	data := h.page(r, "Threads", "threads")
	data.Threads = threads
	data.FilterAgent = agentID
	h.render(w, "threads.html", data)
}

func (h *Handler) threadDetail(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("id")

	// We need to find the thread across all accounts for admin view
	threads, err := h.agentRepo.ListAllThreads(r.Context(), "", nil)
	if err != nil {
		http.Error(w, "failed to list threads", http.StatusInternalServerError)
		return
	}
	var thread *models.Thread
	for i := range threads {
		if threads[i].ID == threadID {
			thread = &threads[i]
			break
		}
	}
	if thread == nil {
		http.NotFound(w, r)
		return
	}

	events, err := h.memoryRepo.ListEventsByThreadID(r.Context(), threadID)
	if err != nil {
		slog.Error("list events", "error", err)
		http.Error(w, "failed to list events", http.StatusInternalServerError)
		return
	}

	facts, err := h.memoryRepo.ListFactsByThreadID(r.Context(), threadID)
	if err != nil {
		slog.Error("list facts", "error", err)
		http.Error(w, "failed to list facts", http.StatusInternalServerError)
		return
	}

	data := h.page(r, "Thread Detail", "threads")
	data.Thread = thread
	data.Events = events
	data.Facts = facts
	h.render(w, "thread_detail.html", data)
}

func (h *Handler) deleteThread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if _, err := h.agentRepo.DeleteThreadByID(r.Context(), accountID, id); err != nil {
		slog.Error("delete thread", "error", err)
		http.Error(w, "failed to delete thread", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/threads", http.StatusSeeOther)
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.userRepo.ListAll(r.Context())
	if err != nil {
		slog.Error("list users", "error", err)
		http.Error(w, "failed to list users", http.StatusInternalServerError)
		return
	}
	data := h.page(r, "Users", "users")
	data.Users = users
	h.render(w, "users.html", data)
}

func (h *Handler) updateUserRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	role := strings.TrimSpace(r.FormValue("role"))
	if role != "admin" && role != "user" {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}
	if err := h.userRepo.UpdateRole(r.Context(), id, role); err != nil {
		slog.Error("update user role", "error", err)
		http.Error(w, "failed to update role", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		user, err := h.userRepo.GetByID(r.Context(), id)
		if err != nil || user == nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		h.renderUserRow(w, user)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.userRepo.Delete(r.Context(), id); err != nil {
		slog.Error("delete user", "error", err)
		http.Error(w, "failed to delete user", http.StatusInternalServerError)
		return
	}
	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// --- HTMX partial renderers ---

func (h *Handler) renderAccountRow(w http.ResponseWriter, acct *models.Account) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := `<tr id="account-{{.ID}}">
	<td class="px-4 py-3 font-mono text-xs text-gray-600">{{.ID}}</td>
	<td class="px-4 py-3 text-gray-900">{{.Name}}</td>
	<td class="px-4 py-3 text-gray-500">{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
	<td class="px-4 py-3 text-right">
		<button hx-delete="/admin/accounts/{{.ID}}" hx-target="#account-{{.ID}}" hx-swap="outerHTML swap:0.3s"
				hx-confirm="Delete this account?" class="text-xs text-red-600 hover:text-red-800 font-medium">Delete</button>
	</td>
</tr>`
	t := template.Must(template.New("row").Parse(tmpl))
	_ = t.Execute(w, acct)
}

func (h *Handler) renderAgentRow(w http.ResponseWriter, agent *models.Agent) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := `<tr id="agent-{{.ID}}">
	<td class="px-4 py-3 font-mono text-xs text-gray-600">{{.ID}}</td>
	<td class="px-4 py-3 text-gray-900">{{.Name}}</td>
	<td class="px-4 py-3 font-mono text-xs text-gray-500">{{.AccountID}}</td>
	<td class="px-4 py-3 text-gray-500">{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
	<td class="px-4 py-3 text-right space-x-3">
		<a href="/admin/threads?agent_id={{.ID}}" class="text-xs text-blue-600 hover:text-blue-800 font-medium">Threads</a>
		<button hx-delete="/admin/agents/{{.ID}}?account_id={{.AccountID}}" hx-target="#agent-{{.ID}}" hx-swap="outerHTML swap:0.3s"
				hx-confirm="Delete this agent?" class="text-xs text-red-600 hover:text-red-800 font-medium">Delete</button>
	</td>
</tr>`
	t := template.Must(template.New("row").Parse(tmpl))
	_ = t.Execute(w, agent)
}

func (h *Handler) renderUserRow(w http.ResponseWriter, user *models.User) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := `<tr id="user-{{.ID}}">
	<td class="px-4 py-3">
		<div class="flex items-center gap-2">
			{{if .Picture}}<img src="{{.Picture}}" class="w-6 h-6 rounded-full" alt="" referrerpolicy="no-referrer">{{end}}
			<span class="text-gray-900">{{.Name}}</span>
		</div>
	</td>
	<td class="px-4 py-3 text-gray-600">{{.Email}}</td>
	<td class="px-4 py-3">
		<form hx-put="/admin/users/{{.ID}}/role" hx-target="#user-{{.ID}}" hx-swap="outerHTML">
			<select name="role" onchange="this.form.requestSubmit()"
					class="text-xs border border-gray-300 rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-blue-500
						   {{if eq .Role "admin"}}bg-indigo-50 text-indigo-700{{else}}bg-gray-50 text-gray-700{{end}}">
				<option value="admin" {{if eq .Role "admin"}}selected{{end}}>admin</option>
				<option value="user" {{if eq .Role "user"}}selected{{end}}>user</option>
			</select>
		</form>
	</td>
	<td class="px-4 py-3 text-gray-500">{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
	<td class="px-4 py-3 text-right">
		<button hx-delete="/admin/users/{{.ID}}" hx-target="#user-{{.ID}}" hx-swap="outerHTML swap:0.3s"
				hx-confirm="Delete this user?" class="text-xs text-red-600 hover:text-red-800 font-medium">Delete</button>
	</td>
</tr>`
	t := template.Must(template.New("row").Parse(tmpl))
	_ = t.Execute(w, user)
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// --- Playground ---

type PlaygroundResult struct {
	Op    string
	Facts []models.ReturnedFact
	Error string
}

func (h *Handler) playgroundPage(w http.ResponseWriter, r *http.Request) {
	accounts, _ := h.accountRepo.ListAllAccounts(r.Context())
	data := h.page(r, "Memory Playground", "playground")
	data.Accounts = accounts
	h.render(w, "playground.html", data)
}

func (h *Handler) playgroundAgents(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	agents, _ := h.agentRepo.ListAllAgents(r.Context(), accountID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t := template.Must(template.New("opts").Parse(
		`<option value="">— select agent —</option>{{range .}}<option value="{{.ID}}">{{.Name}} ({{.ID}})</option>{{end}}`,
	))
	_ = t.Execute(w, agents)
}

func (h *Handler) playgroundThreads(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	agentIDPtr := &agentID
	threads, _ := h.agentRepo.ListAllThreads(r.Context(), "", agentIDPtr)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t := template.Must(template.New("opts").Parse(
		`<option value="">— no thread —</option>{{range .}}<option value="{{.ID}}">{{.ID}} ({{.CreatedAt.Format "Jan 2 15:04"}})</option>{{end}}`,
	))
	_ = t.Execute(w, threads)
}

func parseInputItems(r *http.Request) []models.InputItem {
	if err := r.ParseForm(); err != nil {
		return nil
	}
	kinds := r.Form["kind"]
	contents := r.Form["content"]
	var items []models.InputItem
	for i, c := range contents {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		kind := models.SOURCE_USER
		if i < len(kinds) {
			if k := models.SourceKind(strings.TrimSpace(kinds[i])); k != "" {
				kind = k
			}
		}
		items = append(items, models.InputItem{Kind: kind, Content: c, ContentType: "text/plain"})
	}
	return items
}

func (h *Handler) playgroundContextual(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	threadID := strings.TrimSpace(r.FormValue("thread_id"))
	inputs := parseInputItems(r)

	if accountID == "" || agentID == "" || len(inputs) == 0 {
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Contextual", Error: "account, agent, and at least one input are required"})
		return
	}

	input := models.MemoryInput{
		AccountID: accountID,
		AgentID:   agentID,
		ThreadID:  threadID,
		Inputs:    inputs,
	}
	out, err := h.engine.ProcessContextual(r.Context(), input)
	if err != nil {
		slog.Error("playground contextual", "error", err)
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Contextual", Error: fmt.Sprintf("engine error: %v", err)})
		return
	}
	h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Contextual", Facts: out.Facts})
}

func (h *Handler) playgroundFactual(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	threadID := strings.TrimSpace(r.FormValue("thread_id"))
	inputs := parseInputItems(r)

	if accountID == "" || agentID == "" || len(inputs) == 0 {
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Factual", Error: "account, agent, and at least one input are required"})
		return
	}

	input := models.FactualInput{
		AccountID: accountID,
		AgentID:   agentID,
		ThreadID:  threadID,
		Inputs:    inputs,
	}
	out, err := h.engine.AddFactual(r.Context(), input)
	if err != nil {
		slog.Error("playground factual", "error", err)
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Factual", Error: fmt.Sprintf("engine error: %v", err)})
		return
	}
	h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Factual", Facts: out.Facts})
}

func (h *Handler) playgroundRecall(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	threadID := strings.TrimSpace(r.FormValue("thread_id"))
	query := strings.TrimSpace(r.FormValue("query"))
	limitStr := strings.TrimSpace(r.FormValue("limit"))
	includeSources := r.FormValue("include_sources") == "on"

	if accountID == "" || agentID == "" || query == "" {
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Recall", Error: "account, agent, and query are required"})
		return
	}

	limit := 10
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}

	input := models.RecallInput{
		AccountID:      accountID,
		AgentID:        agentID,
		ThreadID:       threadID,
		Query:          query,
		Limit:          limit,
		IncludeSources: includeSources,
	}
	out, err := h.engine.Recall(r.Context(), input)
	if err != nil {
		slog.Error("playground recall", "error", err)
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Recall", Error: fmt.Sprintf("engine error: %v", err)})
		return
	}
	h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Recall", Facts: out.Facts})
}

func (h *Handler) renderPlaygroundResult(w http.ResponseWriter, result *PlaygroundResult) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmplStr := `{{define "result"}}
<div id="playground-result" class="mt-6">
  {{if .Error}}
  <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
    <span class="font-medium">Error:</span> {{.Error}}
  </div>
  {{else}}
  <div class="rounded-lg border border-gray-200 bg-white overflow-hidden">
    <div class="flex items-center justify-between px-4 py-2 bg-gray-50 border-b border-gray-200">
      <span class="text-xs font-medium text-gray-600">{{.Op}} — {{len .Facts}} fact(s) returned</span>
    </div>
    {{if .Facts}}
    <ul class="divide-y divide-gray-100">
      {{range .Facts}}
      <li class="px-4 py-3">
        <div class="flex items-start gap-3">
          <span class="inline-flex shrink-0 px-2 py-0.5 text-xs font-medium rounded-full mt-0.5
            {{if eq (printf "%s" .Kind) "KNOWLEDGE"}}bg-blue-100 text-blue-700
            {{else if eq (printf "%s" .Kind) "RULE"}}bg-purple-100 text-purple-700
            {{else}}bg-amber-100 text-amber-700{{end}}">{{.Kind}}</span>
          <span class="inline-flex shrink-0 px-2 py-0.5 text-xs font-medium rounded-full mt-0.5
            {{if eq (printf "%s" .SourceKind) "text"}}bg-gray-100 text-gray-600
            {{else if eq (printf "%s" .SourceKind) "tool"}}bg-teal-100 text-teal-700
            {{else}}bg-orange-100 text-orange-700{{end}}">{{.SourceKind}}</span>
          <p class="text-sm text-gray-900 flex-1">{{.Text}}</p>
        </div>
        <p class="mt-1 text-xs font-mono text-gray-400 pl-0">{{.ID}}</p>
        {{if .OriginalSource}}
        <p class="mt-1 text-xs text-gray-500 italic pl-0">Source: {{.OriginalSource}}</p>
        {{end}}
      </li>
      {{end}}
    </ul>
    {{else}}
    <p class="px-4 py-3 text-sm text-gray-400">No facts returned.</p>
    {{end}}
  </div>
  {{end}}
</div>
{{end}}`
	t := template.Must(template.New("result").Parse(tmplStr))
	_ = t.ExecuteTemplate(w, "result", result)
}
