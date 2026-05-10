package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type locomoTurn struct {
	Speaker     string   `json:"speaker"`
	DiaID       string   `json:"dia_id"`
	Text        string   `json:"text"`
	ImgURL      []string `json:"img_url"`
	BlipCaption string   `json:"blip_caption"`
}

type locomoQA struct {
	Question string   `json:"question"`
	Answer   any      `json:"answer"`
	Evidence []string `json:"evidence"`
	Category int      `json:"category"`
}

type locomoSession struct {
	Number   int
	DateTime string
	Turns    []locomoTurn
}

type locomoSample struct {
	SampleID string
	Sessions []locomoSession
	QA       []locomoQA
	// dia_id → session date string
	diaDate map[string]string
}

// LocomoHit is a single search result.
type LocomoHit struct {
	SampleID    string        `json:"sample_id"`
	DiaID       string        `json:"dia_id"`
	Speaker     string        `json:"speaker"`
	Text        string        `json:"text"`
	ImgURL      []string      `json:"img_url"`
	BlipCaption string        `json:"blip_caption,omitempty"`
	SessionDate string        `json:"session_date"`
	QARefs      []locomoQARef `json:"qa_refs"`
}

type locomoQARef struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// LocomoStore lazy-loads and caches the parsed locomo dataset.
type LocomoStore struct {
	path        string
	mu          sync.RWMutex
	samples     []locomoSample
	diaByText   map[string]string // turn text → dia_id
}

func NewLocomoStore(path string) *LocomoStore {
	return &LocomoStore{path: path}
}

func (s *LocomoStore) load() ([]locomoSample, error) {
	s.mu.RLock()
	if s.samples != nil {
		result := s.samples
		s.mu.RUnlock()
		return result, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.samples != nil {
		return s.samples, nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read locomo dataset: %w", err)
	}
	samples, err := parseLocomoDataset(data)
	if err != nil {
		return nil, fmt.Errorf("parse locomo dataset: %w", err)
	}
	s.samples = samples
	s.diaByText = buildDiaByTextIndex(samples)
	slog.Info("locomo dataset loaded", "samples", len(samples))
	return samples, nil
}

func buildDiaByTextIndex(samples []locomoSample) map[string]string {
	idx := make(map[string]string)
	for i := range samples {
		for si := range samples[i].Sessions {
			for ti := range samples[i].Sessions[si].Turns {
				t := &samples[i].Sessions[si].Turns[ti]
				if t.DiaID == "" {
					continue
				}
				key := strings.TrimSpace(t.Text)
				if key == "" {
					continue
				}
				if _, exists := idx[key]; !exists {
					idx[key] = t.DiaID
				}
			}
		}
	}
	return idx
}

// LookupDiaID returns the dia_id of a locomo turn whose text matches the given content.
func (s *LocomoStore) LookupDiaID(content string) string {
	if _, err := s.load(); err != nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.diaByText[strings.TrimSpace(content)]
}

// ─── Parser ───────────────────────────────────────────────────────────────────

type rawLocomoSample struct {
	SampleID     string                       `json:"sample_id"`
	QA           []locomoQA                   `json:"qa"`
	Conversation map[string]json.RawMessage   `json:"conversation"`
}

var reSessKey = regexp.MustCompile(`^session_(\d+)$`)

func parseLocomoDataset(data []byte) ([]locomoSample, error) {
	var raws []rawLocomoSample
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}

	samples := make([]locomoSample, 0, len(raws))
	for _, r := range raws {
		sample := locomoSample{
			SampleID: r.SampleID,
			QA:       r.QA,
			diaDate:  make(map[string]string),
		}

		// Collect session date-times and find the highest session number.
		dateTimes := map[int]string{}
		maxSess := 0
		for k, v := range r.Conversation {
			if strings.HasSuffix(k, "_date_time") {
				base := strings.TrimSuffix(k, "_date_time")
				if m := reSessKey.FindStringSubmatch(base); m != nil {
					n, _ := strconv.Atoi(m[1])
					var dt string
					_ = json.Unmarshal(v, &dt)
					dateTimes[n] = dt
					if n > maxSess {
						maxSess = n
					}
				}
			}
			if m := reSessKey.FindStringSubmatch(k); m != nil {
				if n, _ := strconv.Atoi(m[1]); n > maxSess {
					maxSess = n
				}
			}
		}

		for i := 1; i <= maxSess; i++ {
			raw, ok := r.Conversation[fmt.Sprintf("session_%d", i)]
			if !ok {
				continue
			}
			var turns []locomoTurn
			if err := json.Unmarshal(raw, &turns); err != nil {
				continue
			}
			sess := locomoSession{
				Number:   i,
				DateTime: dateTimes[i],
				Turns:    turns,
			}
			for _, t := range turns {
				if t.DiaID != "" {
					sample.diaDate[t.DiaID] = dateTimes[i]
				}
			}
			sample.Sessions = append(sample.Sessions, sess)
		}

		samples = append(samples, sample)
	}
	return samples, nil
}

// ─── Search ───────────────────────────────────────────────────────────────────

var (
	// Matches a bare dia_id with no colon: D112 → D1:12 (first digit = session, rest = turn).
	reDiaNoColon = regexp.MustCompile(`(?i)^D(\d)(\d+)$`)
	// Matches an already-formatted dia_id: D1:12.
	reDiaFull = regexp.MustCompile(`(?i)^D\d+:\d+$`)
)

func normalizeDiaID(q string) string {
	q = strings.TrimSpace(q)
	if reDiaFull.MatchString(q) {
		return strings.ToUpper(q)
	}
	if m := reDiaNoColon.FindStringSubmatch(q); m != nil {
		return fmt.Sprintf("D%s:%s", strings.ToUpper(m[1]), m[2])
	}
	return ""
}

func (s *LocomoStore) Search(q string) ([]LocomoHit, error) {
	samples, err := s.load()
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	qLower := strings.ToLower(q)
	normalizedDia := normalizeDiaID(q)

	var hits []LocomoHit
	for i := range samples {
		sample := &samples[i]

		// Build dia_id → turn and dia_id → session date indexes.
		turnByDia := map[string]*locomoTurn{}
		dateByDia := map[string]string{}
		for si := range sample.Sessions {
			sess := &sample.Sessions[si]
			for ti := range sess.Turns {
				t := &sess.Turns[ti]
				if t.DiaID != "" {
					turnByDia[t.DiaID] = t
					dateByDia[t.DiaID] = sess.DateTime
				}
			}
		}

		// Build dia_id → QA refs index.
		qaByDia := map[string][]locomoQARef{}
		for _, qa := range sample.QA {
			ref := locomoQARef{
				Question: qa.Question,
				Answer:   fmt.Sprintf("%v", qa.Answer),
			}
			for _, eid := range qa.Evidence {
				qaByDia[eid] = append(qaByDia[eid], ref)
			}
		}

		// Collect dia_ids to emit, avoiding duplicates.
		seen := map[string]bool{}
		emit := func(diaID string) {
			if seen[diaID] {
				return
			}
			seen[diaID] = true
			turn, ok := turnByDia[diaID]
			if !ok {
				return
			}
			hits = append(hits, LocomoHit{
				SampleID:    sample.SampleID,
				DiaID:       turn.DiaID,
				Speaker:     turn.Speaker,
				Text:        turn.Text,
				ImgURL:      turn.ImgURL,
				BlipCaption: turn.BlipCaption,
				SessionDate: dateByDia[diaID],
				QARefs:      qaByDia[diaID],
			})
		}

		// Match QA questions — emit all their evidence turns.
		for _, qa := range sample.QA {
			if strings.Contains(strings.ToLower(qa.Question), qLower) {
				for _, eid := range qa.Evidence {
					emit(eid)
				}
			}
		}

		// Match turn text or dia_id directly.
		for _, sess := range sample.Sessions {
			for _, turn := range sess.Turns {
				matched := normalizedDia != "" && strings.EqualFold(turn.DiaID, normalizedDia)
				if !matched && turn.Text != "" {
					matched = strings.Contains(strings.ToLower(turn.Text), qLower)
				}
				if matched {
					emit(turn.DiaID)
				}
			}
		}
	}
	return hits, nil
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (h *Handler) locomoPage(w http.ResponseWriter, r *http.Request) {
	accounts, _ := h.accountRepo.ListAllAccounts(r.Context())
	data := h.page(r, "Locomo Explorer", "locomo")
	data.Accounts = accounts
	data.LocomoQuery = strings.TrimSpace(r.URL.Query().Get("q"))
	data.LocomoSourceID = strings.TrimSpace(r.URL.Query().Get("source_id"))
	data.LocomoAccountID = strings.TrimSpace(r.URL.Query().Get("account_id"))
	data.LocomoAgentID = strings.TrimSpace(r.URL.Query().Get("agent_id"))
	data.LocomoThreadID = strings.TrimSpace(r.URL.Query().Get("thread_id"))

	if data.LocomoAccountID != "" {
		if agents, err := h.agentRepo.ListAllAgents(r.Context(), data.LocomoAccountID); err == nil {
			data.Agents = agents
		}
	}
	if data.LocomoAgentID != "" {
		agentIDPtr := data.LocomoAgentID
		if threads, err := h.agentRepo.ListAllThreads(r.Context(), "", &agentIDPtr); err == nil {
			data.Threads = threads
		}
	}

	h.render(w, "locomo.html", data)
}

func (h *Handler) locomoSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	hits, err := h.locomoStore.Search(q)
	if err != nil {
		slog.Error("locomo search", "error", err)
		http.Error(w, "failed to search locomo dataset: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if hits == nil {
		hits = []LocomoHit{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
}

type locomoRecallReq struct {
	AccountID string `json:"account_id"`
	AgentID   string `json:"agent_id"`
	ThreadID  string `json:"thread_id"`
	Text      string `json:"text"`
}

type locomoFactResult struct {
	ID           string  `json:"id"`
	Text         string  `json:"text"`
	Kind         string  `json:"kind"`
	SourceID     string  `json:"source_id"`
	ThreadID     *string `json:"thread_id"`
	SupersededBy *string `json:"superseded_by"`
	SupersededAt *string `json:"superseded_at"`
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// locomoRecall finds sources whose content starts with the given turn text,
// then returns all facts linked to those source IDs — no LLM or recall pipeline involved.
func (h *Handler) locomoRecall(w http.ResponseWriter, r *http.Request) {
	var req locomoRecallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.AccountID == "" || req.AgentID == "" || req.Text == "" {
		jsonError(w, "account_id, agent_id, and text are required", http.StatusBadRequest)
		return
	}

	sources, err := h.memoryRepo.SearchSourcesByContent(r.Context(), req.AccountID, req.AgentID, req.ThreadID, req.Text)
	if err != nil {
		slog.Error("locomo source search", "error", err)
		jsonError(w, "source lookup error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sourceIDs := make([]string, len(sources))
	for i, s := range sources {
		sourceIDs[i] = s.ID
	}

	facts, err := h.memoryRepo.ListFactsBySourceIDs(r.Context(), req.AccountID, sourceIDs)
	if err != nil {
		slog.Error("locomo facts by source", "error", err)
		jsonError(w, "facts lookup error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out := make([]locomoFactResult, len(facts))
	for i, f := range facts {
		var supersededAt *string
		if f.SupersededAt != nil {
			s := f.SupersededAt.Format("2006-01-02")
			supersededAt = &s
		}
		out[i] = locomoFactResult{
			ID:           f.ID,
			Text:         f.Text,
			Kind:         string(f.Kind),
			SourceID:     f.SourceID,
			ThreadID:     f.ThreadID,
			SupersededBy: f.SupersededBy,
			SupersededAt: supersededAt,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"facts": out})
}
