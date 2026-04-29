package admin

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentmem/internal/account"
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
	accountSvc  *account.Service
	agentRepo   agentrepo.Repository
	memoryRepo  memoryrepo.Repository
	userRepo    userrepo.Repository
	engine      *engine.MemoryEngine
}

func NewHandler(
	accountRepo accountrepo.Repository,
	accountSvc *account.Service,
	agentRepo agentrepo.Repository,
	memoryRepo memoryrepo.Repository,
	userRepo userrepo.Repository,
	eng *engine.MemoryEngine,
) *Handler {
	return &Handler{
		accountRepo: accountRepo,
		accountSvc:  accountSvc,
		agentRepo:   agentRepo,
		memoryRepo:  memoryRepo,
		userRepo:    userRepo,
		engine:      eng,
	}
}

// tmpl builds a fresh template set for each render to avoid block name conflicts
// when multiple page templates define the same "content" block.
func tmpl(page string) *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"not":         func(b bool) bool { return !b },
		"derefString": func(s *string) string { return *s },
		"derefTimeStr": func(t *time.Time) string { return t.Format("2006-01-02") },
	}).ParseFS(templateFS, "templates/layout.html", "templates/"+page))
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux, adminMw func(http.Handler) http.Handler) {
	mux.HandleFunc("GET /admin/login", h.loginPage)

	protected := http.NewServeMux()
	protected.HandleFunc("GET /admin/", h.dashboard)
	protected.HandleFunc("GET /admin/accounts", h.listAccounts)
	protected.HandleFunc("POST /admin/accounts", h.createAccount)
	protected.HandleFunc("DELETE /admin/accounts/{id}", h.deleteAccount)
	protected.HandleFunc("GET /admin/accounts/{id}", h.accountDetail)
	protected.HandleFunc("POST /admin/accounts/{id}/api-keys", h.createAPIKey)
	protected.HandleFunc("DELETE /admin/accounts/{id}/api-keys/{key_id}", h.revokeAPIKey)
	protected.HandleFunc("GET /admin/agents", h.listAgents)
	protected.HandleFunc("POST /admin/agents", h.createAgent)
	protected.HandleFunc("PUT /admin/agents/{id}", h.updateAgent)
	protected.HandleFunc("DELETE /admin/agents/{id}", h.deleteAgent)
	protected.HandleFunc("GET /admin/threads", h.listThreads)
	protected.HandleFunc("GET /admin/threads/{id}", h.threadDetail)
	protected.HandleFunc("DELETE /admin/threads/{id}", h.deleteThread)
	protected.HandleFunc("POST /admin/threads/{id}/similarity", h.threadSimilarity)
	protected.HandleFunc("GET /admin/users", h.listUsers)
	protected.HandleFunc("PUT /admin/users/{id}/role", h.updateUserRole)
	protected.HandleFunc("DELETE /admin/users/{id}", h.deleteUser)
	protected.HandleFunc("GET /admin/playground", h.playgroundPage)
	protected.HandleFunc("GET /admin/playground/agents", h.playgroundAgents)
	protected.HandleFunc("GET /admin/playground/threads", h.playgroundThreads)
	protected.HandleFunc("POST /admin/playground/contextual", h.playgroundContextual)
	protected.HandleFunc("POST /admin/playground/factual", h.playgroundFactual)
	protected.HandleFunc("POST /admin/playground/recall", h.playgroundRecall)
	protected.HandleFunc("POST /admin/playground/decompose", h.playgroundDecompose)

	mux.Handle("/admin/", adminMw(protected))
}

type pageData struct {
	Title string
	Nav   string
	User  *models.User
	Flash string

	// page-specific
	Accounts      []models.Account
	Account       *models.Account
	APIKeys       []models.APIKey
	NewAPIKey     *newAPIKeyResult
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

type newAPIKeyResult struct {
	Key      *models.APIKey
	Plaintext string
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

func (h *Handler) accountDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	acct, err := h.accountRepo.GetAccountByID(r.Context(), id)
	if err != nil {
		slog.Error("get account", "error", err)
		http.NotFound(w, r)
		return
	}
	keys, err := h.accountSvc.ListAPIKeysByAccountID(r.Context(), id)
	if err != nil {
		slog.Error("list api keys", "error", err)
		http.Error(w, "failed to list api keys", http.StatusInternalServerError)
		return
	}
	data := h.page(r, "Account: "+acct.Name, "accounts")
	data.Account = acct
	data.APIKeys = keys
	h.render(w, "account_detail.html", data)
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	label := strings.TrimSpace(r.FormValue("label"))
	expiresAtRaw := strings.TrimSpace(r.FormValue("expires_at"))

	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}

	var expiresAt *time.Time
	if expiresAtRaw != "" {
		parsed, err := time.Parse("2006-01-02", expiresAtRaw)
		if err == nil {
			utc := parsed.UTC()
			expiresAt = &utc
		}
	}

	key, plaintext, err := h.accountSvc.CreateAPIKey(r.Context(), id, labelPtr, expiresAt)
	if err != nil {
		slog.Error("create api key", "error", err)
		http.Error(w, "failed to create api key", http.StatusInternalServerError)
		return
	}

	keys, err := h.accountSvc.ListAPIKeysByAccountID(r.Context(), id)
	if err != nil {
		slog.Error("list api keys after create", "error", err)
		http.Error(w, "failed to list api keys", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		data := apiKeysSectionData{
			AccountID: id,
			Keys:      keys,
			NewAPIKey: &newAPIKeyResult{Key: key, Plaintext: plaintext},
		}
		h.renderAPIKeysSection(w, data)
		return
	}
	http.Redirect(w, r, "/admin/accounts/"+id, http.StatusSeeOther)
}

func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	keyID := r.PathValue("key_id")

	if err := h.accountSvc.InvalidateAPIKeyByID(r.Context(), keyID); err != nil {
		slog.Error("revoke api key", "error", err)
		http.Error(w, "failed to revoke api key", http.StatusInternalServerError)
		return
	}

	keys, err := h.accountSvc.ListAPIKeysByAccountID(r.Context(), id)
	if err != nil {
		slog.Error("list api keys after revoke", "error", err)
		http.Error(w, "failed to list api keys", http.StatusInternalServerError)
		return
	}

	if isHTMX(r) {
		data := apiKeysSectionData{
			AccountID: id,
			Keys:      keys,
		}
		h.renderAPIKeysSection(w, data)
		return
	}
	http.Redirect(w, r, "/admin/accounts/"+id, http.StatusSeeOther)
}

type apiKeysSectionData struct {
	AccountID string
	Keys      []models.APIKey
	NewAPIKey *newAPIKeyResult
}

func (h *Handler) renderAPIKeysSection(w http.ResponseWriter, data apiKeysSectionData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmplStr := `{{define "api-keys-section"}}
<div id="api-keys-section">
  {{if .NewAPIKey}}
  <div class="mb-4 rounded-lg border border-green-200 bg-green-50 p-4">
    <p class="text-sm font-medium text-green-800 mb-2">API key created. Copy it now — it will not be shown again.</p>
    <div class="flex items-center gap-2">
      <code id="new-key-value" class="flex-1 font-mono text-sm bg-white border border-green-300 rounded px-3 py-2 text-green-900 break-all">{{.NewAPIKey.Plaintext}}</code>
      <button type="button" onclick="copyNewKey()"
              class="shrink-0 px-3 py-2 text-sm font-medium text-white bg-green-600 rounded-md hover:bg-green-700 transition-colors">
        Copy
      </button>
    </div>
    <script>
    function copyNewKey() {
      const val = document.getElementById('new-key-value').textContent;
      navigator.clipboard.writeText(val).then(() => {
        const btn = event.target;
        btn.textContent = 'Copied!';
        setTimeout(() => btn.textContent = 'Copy', 2000);
      });
    }
    </script>
  </div>
  {{end}}
  <form hx-post="/admin/accounts/{{.AccountID}}/api-keys"
        hx-target="#api-keys-section"
        hx-swap="outerHTML"
        class="flex gap-2 mb-4">
    <input type="text" name="label" placeholder="Label (optional)"
           class="px-3 py-1.5 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent">
    <input type="date" name="expires_at" title="Expiry date (optional)"
           class="px-3 py-1.5 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent">
    <button type="submit"
            class="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 transition-colors whitespace-nowrap">
      Create Key
    </button>
  </form>
  {{if .Keys}}
  <div class="bg-white rounded-lg border border-gray-200 overflow-hidden">
    <table class="w-full text-sm">
      <thead class="bg-gray-50 border-b border-gray-200">
        <tr>
          <th class="text-left px-4 py-3 font-medium text-gray-500">Prefix</th>
          <th class="text-left px-4 py-3 font-medium text-gray-500">Label</th>
          <th class="text-left px-4 py-3 font-medium text-gray-500">Status</th>
          <th class="text-left px-4 py-3 font-medium text-gray-500">Expires</th>
          <th class="text-left px-4 py-3 font-medium text-gray-500">Created</th>
          <th class="text-right px-4 py-3 font-medium text-gray-500">Actions</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100">
        {{range .Keys}}
        <tr class="{{if not .Valid}}opacity-50{{end}}">
          <td class="px-4 py-3 font-mono text-xs text-gray-600">{{.Prefix}}</td>
          <td class="px-4 py-3 text-gray-700">{{if .Label}}{{deref .Label}}{{else}}<span class="text-gray-300">—</span>{{end}}</td>
          <td class="px-4 py-3 text-xs">
            {{if .Valid}}<span class="text-green-600 font-medium">Active</span>{{else}}<span class="text-gray-400">Revoked</span>{{end}}
          </td>
          <td class="px-4 py-3 text-xs text-gray-500">
            {{if .ExpiresAt}}{{derefTime .ExpiresAt}}{{else}}<span class="text-gray-300">Never</span>{{end}}
          </td>
          <td class="px-4 py-3 text-xs text-gray-500">{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
          <td class="px-4 py-3 text-right">
            {{if .Valid}}
            <button hx-delete="/admin/accounts/{{$.AccountID}}/api-keys/{{.ID}}"
                    hx-target="#api-keys-section"
                    hx-swap="outerHTML"
                    hx-confirm="Revoke this API key? It will stop working immediately."
                    class="text-xs text-red-600 hover:text-red-800 font-medium">Revoke</button>
            {{end}}
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
  {{else}}
  <p class="text-sm text-gray-400">No API keys yet.</p>
  {{end}}
</div>
{{end}}`
	t := template.Must(template.New("api-keys-section").Funcs(template.FuncMap{
		"not":      func(b bool) bool { return !b },
		"deref":    func(s *string) string { return *s },
		"derefTime": func(t *time.Time) string { return t.Format("2006-01-02") },
	}).Parse(tmplStr))
	_ = t.ExecuteTemplate(w, "api-keys-section", data)
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

type factRow struct {
	models.Fact
	Score string // formatted score, or "" if not scored
}

func (h *Handler) threadSimilarity(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("id")
	query := strings.TrimSpace(r.FormValue("query"))

	threads, err := h.agentRepo.ListAllThreads(r.Context(), "", nil)
	if err != nil {
		http.Error(w, "failed to load thread", http.StatusInternalServerError)
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

	allFacts, err := h.memoryRepo.ListFactsByThreadID(r.Context(), threadID)
	if err != nil {
		slog.Error("thread similarity list facts", "error", err)
		http.Error(w, "failed to load facts", http.StatusInternalServerError)
		return
	}

	scores := map[string]float64{}
	if query != "" {
		agentID := thread.AgentID
		tid := thread.ID
		scored, err := h.engine.SearchWithScores(r.Context(), query, memoryrepo.SearchByEmbeddingParams{
			AccountID:     thread.AccountID,
			AgentID:       &agentID,
			ThreadID:      &tid,
			MinSimilarity: 0,
			Limit:         200,
		})
		if err != nil {
			slog.Error("thread similarity search", "error", err)
		} else {
			for _, fs := range scored {
				scores[fs.ID] = fs.Score
			}
		}
	}

	// Build rows sorted by score desc (scored facts first, then unsuperseded, then superseded).
	rows := make([]factRow, 0, len(allFacts))
	for _, f := range allFacts {
		row := factRow{Fact: f}
		if s, ok := scores[f.ID]; ok {
			row.Score = fmt.Sprintf("%.4f", s)
		}
		rows = append(rows, row)
	}
	if len(scores) > 0 {
		// Sort: scored facts by score desc, unscored at bottom.
		sortFactRows(rows)
	}

	h.renderFactsSection(w, thread, rows, query)
}

func sortFactRows(rows []factRow) {
	scoreVal := func(r factRow) float64 {
		if r.Score == "" {
			return -1
		}
		var v float64
		fmt.Sscanf(r.Score, "%f", &v)
		return v
	}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if scoreVal(rows[j]) > scoreVal(rows[i]) {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
}

func (h *Handler) renderFactsSection(w http.ResponseWriter, thread *models.Thread, rows []factRow, query string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmplStr := `{{define "facts-section"}}
<div id="facts-section">
  <div class="flex items-center justify-between mb-3">
    <h3 class="text-md font-semibold text-gray-800">Facts ({{len .Rows}})</h3>
    <form hx-post="/admin/threads/{{.ThreadID}}/similarity"
          hx-target="#facts-section"
          hx-swap="outerHTML"
          hx-indicator="#spinner-sim"
          class="flex gap-2 items-center">
      <input name="query" type="text" value="{{.Query}}" placeholder="Similarity check…"
             class="w-64 border border-gray-300 rounded-md px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <button type="submit"
              class="inline-flex items-center gap-1.5 bg-blue-600 text-white text-sm font-medium px-3 py-1.5 rounded-md hover:bg-blue-700 transition-colors whitespace-nowrap">
        <span id="spinner-sim" class="htmx-indicator">
          <svg class="animate-spin w-3.5 h-3.5" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"></path>
          </svg>
        </span>
        Check
      </button>
    </form>
  </div>
  {{if .Rows}}
  <div class="bg-white rounded-lg border border-gray-200 overflow-hidden">
    <table class="w-full text-sm">
      <thead class="bg-gray-50 border-b border-gray-200">
        <tr>
          <th class="text-left px-4 py-2 font-medium text-gray-500">ID</th>
          <th class="text-left px-4 py-2 font-medium text-gray-500">Kind</th>
          <th class="text-left px-4 py-2 font-medium text-gray-500 w-1/2">Text</th>
          <th class="text-left px-4 py-2 font-medium text-gray-500">Status</th>
          <th class="text-left px-4 py-2 font-medium text-gray-500">Ref Date</th>
          <th class="text-left px-4 py-2 font-medium text-gray-500">Created</th>
          <th class="text-right px-4 py-2 font-medium text-gray-500">Score</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100">
        {{range .Rows}}
        <tr class="{{if .SupersededAt}}opacity-50{{end}}">
          <td class="px-4 py-2 font-mono text-xs text-gray-600">{{.ID}}</td>
          <td class="px-4 py-2">
            <span class="inline-flex px-2 py-0.5 text-xs font-medium rounded-full
              {{if eq (printf "%s" .Kind) "KNOWLEDGE"}}bg-blue-100 text-blue-700
              {{else if eq (printf "%s" .Kind) "RULE"}}bg-purple-100 text-purple-700
              {{else}}bg-amber-100 text-amber-700{{end}}">{{.Kind}}</span>
          </td>
          <td class="px-4 py-2 text-gray-900 max-w-md truncate">{{.Text}}</td>
          <td class="px-4 py-2 text-xs">
            {{if .SupersededAt}}<span class="text-orange-600">Superseded</span>
            {{else}}<span class="text-green-600">Active</span>{{end}}
          </td>
          <td class="px-4 py-2 text-xs font-mono text-indigo-600">
            {{if .ReferencedAt}}{{.ReferencedAt.Format "2006-01-02"}}{{else}}<span class="text-gray-300">—</span>{{end}}
          </td>
          <td class="px-4 py-2 text-gray-500 text-xs">{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
          <td class="px-4 py-2 text-right font-mono text-xs text-gray-600">{{.Score}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </div>
  {{else}}
  <p class="text-sm text-gray-400">No facts</p>
  {{end}}
</div>
{{end}}`
	type data struct {
		ThreadID string
		Query    string
		Rows     []factRow
	}
	t := template.Must(template.New("facts-section").Parse(tmplStr))
	_ = t.ExecuteTemplate(w, "facts-section", data{ThreadID: thread.ID, Query: query, Rows: rows})
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
	<td class="px-4 py-3 font-mono text-xs text-gray-600"><a href="/admin/accounts/{{.ID}}" class="hover:underline text-blue-600">{{.ID}}</a></td>
	<td class="px-4 py-3 text-gray-900"><a href="/admin/accounts/{{.ID}}" class="hover:underline">{{.Name}}</a></td>
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
	Op          string
	Facts       []models.ReturnedFact
	Error       string
	NewThreadID string
}

type DecomposeResult struct {
	Mode    string
	Facts   []models.ExtractedFact
	Queries []models.ExtractedQuery
	Error   string
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
		`<option value="__new__">✦ New thread</option><option value="">— no thread —</option>{{range .}}<option value="{{.ID}}">{{.ID}} ({{.CreatedAt.Format "Jan 2 15:04"}})</option>{{end}}`,
	))
	_ = t.Execute(w, threads)
}

func parseInputItems(r *http.Request) []models.InputItem {
	if err := r.ParseForm(); err != nil {
		return nil
	}
	kinds := r.Form["kind"]
	authors := r.Form["author"]
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
		item := models.InputItem{Kind: kind, Content: c, ContentType: "text/plain"}
		if i < len(authors) {
			if a := strings.TrimSpace(authors[i]); a != "" {
				item.Author = &a
			}
		}
		items = append(items, item)
	}
	return items
}

func (h *Handler) resolveThreadID(ctx context.Context, threadID, accountID, agentID string) (string, string, error) {
	if threadID != "__new__" {
		return threadID, "", nil
	}
	t, err := h.agentRepo.CreateThread(ctx, accountID, agentID)
	if err != nil {
		return "", "", err
	}
	return t.ID, t.ID, nil
}

func (h *Handler) playgroundContextual(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	threadID, newThreadID, err := h.resolveThreadID(r.Context(), strings.TrimSpace(r.FormValue("thread_id")), accountID, agentID)
	if err != nil {
		slog.Error("playground create thread", "error", err)
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Contextual", Error: fmt.Sprintf("failed to create thread: %v", err)})
		return
	}
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
	d := out.Duration
	slog.Info("playground contextual duration", "db_ms", d.DBMs, "db_calls", d.DBCalls, "ai_ms", d.AIMs, "ai_calls", d.AICalls)
	h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Contextual", NewThreadID: newThreadID})
}

func (h *Handler) playgroundFactual(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	threadID, newThreadID, err := h.resolveThreadID(r.Context(), strings.TrimSpace(r.FormValue("thread_id")), accountID, agentID)
	if err != nil {
		slog.Error("playground create thread", "error", err)
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Factual", Error: fmt.Sprintf("failed to create thread: %v", err)})
		return
	}
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
	d := out.Duration
	slog.Info("playground factual duration", "db_ms", d.DBMs, "db_calls", d.DBCalls, "ai_ms", d.AIMs, "ai_calls", d.AICalls)
	h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Factual", NewThreadID: newThreadID})
}

func (h *Handler) playgroundRecall(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	agentID := strings.TrimSpace(r.FormValue("agent_id"))
	threadID := strings.TrimSpace(r.FormValue("thread_id"))
	query := strings.TrimSpace(r.FormValue("query"))
	eventDateRaw := strings.TrimSpace(r.FormValue("event_date"))
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

	var eventDate *time.Time
	if eventDateRaw != "" {
		parsed, err := time.Parse("2006-01-02", eventDateRaw)
		if err != nil {
			h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Recall", Error: "event_date must be in YYYY-MM-DD format"})
			return
		}
		utc := parsed.UTC()
		eventDate = &utc
	}

	input := models.RecallInput{
		AccountID:      accountID,
		AgentID:        agentID,
		ThreadID:       threadID,
		Query:          query,
		EventDate:      eventDate,
		Limit:          limit,
		IncludeSources: includeSources,
	}
	out, err := h.engine.Recall(r.Context(), input)
	if err != nil {
		slog.Error("playground recall", "error", err)
		h.renderPlaygroundResult(w, &PlaygroundResult{Op: "Recall", Error: fmt.Sprintf("engine error: %v", err)})
		return
	}
	d := out.Duration
	slog.Info("playground recall duration", "db_ms", d.DBMs, "db_calls", d.DBCalls, "ai_ms", d.AIMs, "ai_calls", d.AICalls)
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
      <span class="text-xs font-medium text-gray-600">
        {{if eq .Op "Recall"}}{{.Op}} — {{len .Facts}} fact(s) returned{{else}}{{.Op}} — written{{end}}
      </span>
      {{if .NewThreadID}}
      <span class="text-xs text-indigo-600 font-medium font-mono">new thread: {{.NewThreadID}}</span>
      {{end}}
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
    {{else if eq .Op "Recall"}}
    <p class="px-4 py-3 text-sm text-gray-400">No facts returned.</p>
    {{end}}
  </div>
  {{end}}
</div>
{{if .NewThreadID}}
<input id="ctx-thread-search" type="text" value="{{.NewThreadID}}"
       oninput="filterCombo('ctx-thread-select', this.value)"
       onfocus="document.getElementById('ctx-thread-dropdown').classList.remove('hidden')"
       placeholder="Search threads…"
       class="w-full border border-gray-300 rounded-md px-3 py-2 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
       hx-swap-oob="true">
<input id="ctx-thread-value" type="hidden" value="{{.NewThreadID}}" hx-swap-oob="true">
{{end}}
{{end}}`
	t := template.Must(template.New("result").Parse(tmplStr))
	_ = t.ExecuteTemplate(w, "result", result)
}

func (h *Handler) playgroundDecompose(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.FormValue("text"))
	mode := strings.TrimSpace(r.FormValue("mode"))

	if text == "" {
		h.renderDecomposeResult(w, &DecomposeResult{Mode: mode, Error: "text is required"})
		return
	}

	var result DecomposeResult
	result.Mode = mode

	switch mode {
	case "recall":
		decomp, err := h.engine.DecomposeRecall(r.Context(), engine.DecomposeRecallRequest{Content: text})
		if err != nil {
			slog.Error("playground decompose recall", "error", err)
			result.Error = fmt.Sprintf("engine error: %v", err)
		} else {
			result.Queries = decomp.Queries
		}
	case "conversational":
		req := engine.DecomposeRequest{
			SourceKind: models.SOURCE_USER,
			Content:    text,
		}
		decomp, err := h.engine.Decompose(r.Context(), req)
		if err != nil {
			slog.Error("playground decompose conversational", "error", err)
			result.Error = fmt.Sprintf("engine error: %v", err)
		} else {
			result.Facts = decomp.Facts
			queries, qerr := h.engine.DecomposeQueries(r.Context(), req)
			if qerr != nil {
				slog.Error("playground decompose queries", "error", qerr)
				result.Error = fmt.Sprintf("engine error: %v", qerr)
			} else {
				result.Queries = queries
			}
		}
	default: // "content"
		decomp, err := h.engine.Decompose(r.Context(), engine.DecomposeRequest{
			SourceKind: models.SOURCE_DOCUMENT,
			Content:    text,
		})
		if err != nil {
			slog.Error("playground decompose content", "error", err)
			result.Error = fmt.Sprintf("engine error: %v", err)
		} else {
			result.Facts = decomp.Facts
		}
	}

	h.renderDecomposeResult(w, &result)
}

func (h *Handler) renderDecomposeResult(w http.ResponseWriter, result *DecomposeResult) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmplStr := `{{define "decompose"}}
<div id="playground-result" class="mt-6">
  {{if .Error}}
  <div class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
    <span class="font-medium">Error:</span> {{.Error}}
  </div>
  {{else}}
  <div class="rounded-lg border border-gray-200 bg-white overflow-hidden">
    <div class="px-4 py-2 bg-gray-50 border-b border-gray-200">
      <span class="text-xs font-medium text-gray-600">Decompose ({{.Mode}}) — {{len .Facts}} fact(s), {{len .Queries}} quer(ies)</span>
    </div>
    {{if .Facts}}
    <div class="px-4 py-3 border-b border-gray-100">
      <p class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">Facts</p>
      <ul class="space-y-2">
        {{range .Facts}}
        <li class="flex items-start gap-2">
          <span class="inline-flex shrink-0 px-2 py-0.5 text-xs font-medium rounded-full mt-0.5
            {{if eq (printf "%s" .Kind) "KNOWLEDGE"}}bg-blue-100 text-blue-700
            {{else if eq (printf "%s" .Kind) "RULE"}}bg-purple-100 text-purple-700
            {{else}}bg-amber-100 text-amber-700{{end}}">{{.Kind}}</span>
          <span class="text-sm text-gray-900">{{.Text}}</span>
        </li>
        {{end}}
      </ul>
    </div>
    {{end}}
    {{if .Queries}}
    <div class="px-4 py-3">
      <p class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">Search Queries</p>
      <ul class="space-y-1">
        {{range .Queries}}
        <li class="text-sm text-gray-700 flex items-center gap-2 group">
          <span class="text-gray-400">›</span>
          <span class="flex-1">{{.Text}}</span>
          <button type="button" onclick="copyText(this, {{.Text | js}})"
                  title="Copy"
                  class="opacity-0 group-hover:opacity-100 transition-opacity text-gray-400 hover:text-gray-700 shrink-0">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                    d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/>
            </svg>
          </button>
        </li>
        {{end}}
      </ul>
      <script>
      function copyText(btn, text) {
        navigator.clipboard.writeText(text).then(() => {
          const svg = btn.querySelector('svg');
          svg.innerHTML = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/>';
          btn.classList.add('text-green-500');
          setTimeout(() => {
            svg.innerHTML = '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/>';
            btn.classList.remove('text-green-500');
          }, 1500);
        });
      }
      </script>
    </div>
    {{end}}
    {{if and (not .Facts) (not .Queries)}}
    <p class="px-4 py-3 text-sm text-gray-400">No output.</p>
    {{end}}
  </div>
  {{end}}
</div>
{{end}}`
	t := template.Must(template.New("decompose").Funcs(template.FuncMap{
		"not": func(v interface{}) bool {
			switch val := v.(type) {
			case []models.ExtractedFact:
				return len(val) == 0
			case []models.ExtractedQuery:
				return len(val) == 0
			}
			return true
		},
	}).Parse(tmplStr))
	_ = t.ExecuteTemplate(w, "decompose", result)
}
