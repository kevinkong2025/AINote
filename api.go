package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Server struct {
	store *Store
}

func NewServer(s *Store) *Server { return &Server{store: s} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) RegisterAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/llm/test", s.handleLLMTest)
	mux.HandleFunc("/api/llm/profiles", s.handleProfiles)
	mux.HandleFunc("/api/llm/profiles/", s.handleProfileByAlias)
	mux.HandleFunc("/api/notes", s.handleNotes)
	mux.HandleFunc("/api/notes/", s.handleNoteByID)
	mux.HandleFunc("/api/archive", s.handleArchive)
	mux.HandleFunc("/api/archive/", s.handleArchiveByID)
	mux.HandleFunc("/api/tags", s.handleTags)
	mux.HandleFunc("/api/chats", s.handleChats)
	mux.HandleFunc("/api/chats/", s.handleChatByID)
	mux.HandleFunc("/api/export", s.handleExport)
}

type stateResp struct {
	Notes    []Note         `json:"notes"`
	Archive  []ArchivedNote `json:"archive"`
	Points   []ArchivePoint `json:"points"`
	Chats    []Chat         `json:"chats"`
	Config   Config         `json:"config"`
	DraftSeq int            `json:"draftSeq"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	a, p := s.store.ListArchive()
	cfg := s.store.GetConfig()
	if cfg.Profiles == nil {
		cfg.Profiles = []LLMProfile{}
	}
	if cfg.Presets == nil {
		cfg.Presets = []PresetOption{}
	}
	chats := s.store.ListChats()
	for i := range chats {
		if chats[i].Messages == nil {
			chats[i].Messages = []ChatMessage{}
		}
		if chats[i].NoteNames == nil {
			chats[i].NoteNames = []string{}
		}
		if chats[i].NoteIDs == nil {
			chats[i].NoteIDs = []NoteRef{}
		}
	}
	for i := range a {
		if a[i].Tags == nil {
			a[i].Tags = []string{}
		}
	}
	writeJSON(w, 200, stateResp{
		Notes:   s.store.ListNotes(),
		Archive: a,
		Points:  p,
		Chats:   chats,
		Config:  cfg,
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Theme       *string         `json:"theme"`
		Presets     *[]PresetOption `json:"presets"`
		ActiveAlias *string         `json:"activeAlias"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	cfg := s.store.SaveConfig(func(c *Config) {
		if body.Theme != nil {
			c.Theme = *body.Theme
		}
		if body.Presets != nil {
			c.Presets = *body.Presets
		}
		if body.ActiveAlias != nil {
			c.ActiveAlias = *body.ActiveAlias
		}
	})
	writeJSON(w, 200, cfg)
}

func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, 405, "method not allowed")
		return
	}
	var p LLMProfile
	if err := readJSON(r, &p); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	// validate fields
	if strings.TrimSpace(p.Alias) == "" {
		writeErr(w, 400, "别名不能为空")
		return
	}
	if strings.TrimSpace(p.Model) == "" {
		writeErr(w, 400, "模型名称不能为空")
		return
	}
	if strings.TrimSpace(p.APIKey) == "" {
		writeErr(w, 400, "API Key 不能为空")
		return
	}
	base, err := normalizeBase(p.BaseURL)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	p.BaseURL = base
	if p.Format != "anthropic" {
		p.Format = "openai"
	}
	// step 1: connectivity
	if err := TestConnectivity(base); err != nil {
		writeErr(w, 502, "连接失败: "+err.Error())
		return
	}
	// step 2: simple conversation
	reply, err := SimpleChat(p, []llmMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		writeErr(w, 502, "对话测试失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "true", "reply": truncate(reply, 200)})
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, 405, "method not allowed")
		return
	}
	var p LLMProfile
	if err := readJSON(r, &p); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if p.Format != "anthropic" {
		p.Format = "openai"
	}
	cfg := s.store.SaveConfig(func(c *Config) {
		found := false
		for i := range c.Profiles {
			if c.Profiles[i].Alias == p.Alias {
				c.Profiles[i] = p
				found = true
				break
			}
		}
		if !found {
			c.Profiles = append(c.Profiles, p)
		}
		if c.ActiveAlias == "" {
			c.ActiveAlias = p.Alias
		}
	})
	writeJSON(w, 200, cfg)
}

func (s *Server) handleProfileByAlias(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		writeErr(w, 405, "method not allowed")
		return
	}
	alias := strings.TrimPrefix(r.URL.Path, "/api/llm/profiles/")
	cfg := s.store.SaveConfig(func(c *Config) {
		var np []LLMProfile
		for _, p := range c.Profiles {
			if p.Alias != alias {
				np = append(np, p)
			}
		}
		c.Profiles = np
		if c.ActiveAlias == alias {
			c.ActiveAlias = ""
			if len(np) > 0 {
				c.ActiveAlias = np[0].Alias
			}
		}
	})
	writeJSON(w, 200, cfg)
}

// ---- notes ----

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	_ = readJSON(r, &body)
	n := s.store.CreateNote(body.Kind)
	writeJSON(w, 200, n)
}

func (s *Server) handleNoteByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/notes/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]

	if len(parts) == 2 && parts[1] == "summarize" && r.Method == "POST" {
		s.handleSummarize(w, r, id)
		return
	}

	switch r.Method {
	case "PUT":
		var body struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, "请求格式错误")
			return
		}
		n, ok := s.store.UpdateNote(id, body.Name, body.Content)
		if !ok {
			writeErr(w, 404, "笔记不存在")
			return
		}
		writeJSON(w, 200, n)
	case "DELETE":
		if !s.store.DeleteNote(id) {
			writeErr(w, 404, "笔记不存在")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) activeProfile() (LLMProfile, error) {
	cfg := s.store.GetConfig()
	if cfg.ActiveAlias == "" {
		return LLMProfile{}, fmt.Errorf("尚未配置任何 LLM，请先到设置页添加")
	}
	for _, p := range cfg.Profiles {
		if p.Alias == cfg.ActiveAlias {
			p.BaseURL, _ = normalizeBase(p.BaseURL)
			return p, nil
		}
	}
	return LLMProfile{}, fmt.Errorf("找不到当前模型配置: %s", cfg.ActiveAlias)
}

func (s *Server) handleSummarize(w http.ResponseWriter, r *http.Request, id string) {
	n, ok := s.store.GetNote(id)
	if !ok {
		writeErr(w, 404, "笔记不存在")
		return
	}
	if strings.TrimSpace(n.Content) == "" {
		writeErr(w, 400, "笔记内容为空，无法总结")
		return
	}
	p, err := s.activeProfile()
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	reply, err := SimpleChat(p, []llmMessage{
		{Role: "system", Content: "你是技术笔记总结助手。总结用户提供的笔记，输出结构化的 Markdown，包含：核心内容概述、涉及的技术点、遇到的难点与解决方案、可复用的提示词或经验。"},
		{Role: "user", Content: truncate(n.Content, 60000)},
	})
	if err != nil {
		writeErr(w, 502, "AI 总结失败: "+err.Error())
		return
	}
	name := fmt.Sprintf("AI summary %s %s", time.Now().Format("20060102"), n.Name)
	created := s.store.AddNote(Note{Name: name, Content: reply, Kind: "summary"})
	writeJSON(w, 200, created)
}

// ---- archive ----

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		NoteIDs []string `json:"noteIds"`
		Name    string   `json:"name"`
		Date    string   `json:"date"`
		Tag     string   `json:"tag"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if len(body.NoteIDs) == 0 {
		writeErr(w, 400, "未选择任何笔记")
		return
	}
	pt, added := s.store.ArchiveNotes(body.NoteIDs, body.Name, body.Date, strings.TrimSpace(body.Tag))
	writeJSON(w, 200, map[string]any{"point": pt, "notes": added})
}

func (s *Server) handleArchiveByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/archive/")
	if rest == "tag" || rest == "untag" {
		var body struct {
			IDs []string `json:"ids"`
			Tag string   `json:"tag"`
		}
		if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Tag) == "" {
			writeErr(w, 400, "请求格式错误")
			return
		}
		if rest == "tag" {
			s.store.TagArchived(body.IDs, strings.TrimSpace(body.Tag))
		} else {
			s.store.RemoveTag(body.IDs, strings.TrimSpace(body.Tag))
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	id := rest
	switch r.Method {
	case "PUT":
		var body struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, "请求格式错误")
			return
		}
		n, ok := s.store.UpdateArchived(id, body.Name, body.Content)
		if !ok {
			writeErr(w, 404, "存档不存在")
			return
		}
		writeJSON(w, 200, n)
	case "DELETE":
		if !s.store.DeleteArchived(id) {
			writeErr(w, 404, "存档不存在")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

type tagInfo struct {
	Name     string `json:"name"`
	LastUsed int64  `json:"lastUsed"`
	Count    int    `json:"count"`
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	a, _ := s.store.ListArchive()
	m := map[string]*tagInfo{}
	for _, n := range a {
		for _, t := range n.Tags {
			ti, ok := m[t]
			if !ok {
				ti = &tagInfo{Name: t}
				m[t] = ti
			}
			ti.Count++
			if n.ArchivedAt > ti.LastUsed {
				ti.LastUsed = n.ArchivedAt
			}
		}
	}
	out := make([]tagInfo, 0, len(m))
	for _, ti := range m {
		out = append(out, *ti)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed > out[j].LastUsed })
	writeJSON(w, 200, out)
}

// ---- chats ----

func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Refs    []NoteRef `json:"refs"`
		Alias   string    `json:"alias"`
		Compact bool      `json:"compact"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	var notes []ArchivedNote
	var names []string
	for _, ref := range body.Refs {
		switch ref.Type {
		case "note":
			if n, ok := s.store.GetNote(ref.ID); ok {
				notes = append(notes, ArchivedNote{Name: n.Name, Content: n.Content})
				names = append(names, n.Name)
			}
		case "arch":
			if n, ok := s.store.GetArchived(ref.ID); ok {
				notes = append(notes, n)
				names = append(names, n.Name)
			}
		}
	}
	context := ""
	if body.Compact && len(notes) > 0 {
		if p, err := s.activeProfile(); err == nil {
			if c, err := CompactNotes(p, notes); err == nil && strings.TrimSpace(c) != "" {
				context = c
			}
		}
	}
	if context == "" {
		var sb strings.Builder
		for _, n := range notes {
			sb.WriteString("## " + n.Name + "\n" + truncate(n.Content, 20000) + "\n\n")
		}
		context = sb.String()
	}
	title := ""
	chat := s.store.AddChat(Chat{
		Title:     title,
		Context:   context,
		NoteNames: names,
		NoteIDs:   body.Refs,
		Alias:     body.Alias,
	})
	writeJSON(w, 200, chat)
}

func (s *Server) handleChatByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/chats/")
	parts := strings.Split(rest, "/")
	id := parts[0]

	if len(parts) >= 2 && parts[1] == "send" && r.Method == "POST" {
		s.handleChatSend(w, r, id)
		return
	}
	if len(parts) >= 3 && parts[1] == "messages" && r.Method == "DELETE" {
		s.handleDeleteMessage(w, r, id, parts[2])
		return
	}

	switch r.Method {
	case "PUT":
		var body struct {
			Title string `json:"title"`
			Alias string `json:"alias"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, "请求格式错误")
			return
		}
		c, ok := s.store.UpdateChat(id, func(ch *Chat) {
			if body.Title != "" {
				ch.Title = body.Title
				ch.TitleAuto = false // 用户手动命名后就不再被 AI 自动改名
			}
			if body.Alias != "" {
				ch.Alias = body.Alias
			}
		})
		if !ok {
			writeErr(w, 404, "对话不存在")
			return
		}
		writeJSON(w, 200, c)
	case "DELETE":
		if !s.store.DeleteChat(id) {
			writeErr(w, 404, "对话不存在")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		writeErr(w, 405, "method not allowed")
	}
}

func (s *Server) profileForAlias(alias string) (LLMProfile, error) {
	cfg := s.store.GetConfig()
	if alias == "" {
		alias = cfg.ActiveAlias
	}
	for _, p := range cfg.Profiles {
		if p.Alias == alias {
			p.BaseURL, _ = normalizeBase(p.BaseURL)
			return p, nil
		}
	}
	return LLMProfile{}, fmt.Errorf("找不到模型配置，请先到设置页添加")
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request, chatID string) {
	var body struct {
		Content   string `json:"content"`
		MessageID string `json:"messageId"` // when resubmitting from a given user message
		Alias     string `json:"alias"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeErr(w, 400, "内容为空")
		return
	}
	chat, ok := s.store.GetChat(chatID)
	if !ok {
		writeErr(w, 404, "对话不存在")
		return
	}
	p, err := s.profileForAlias(firstNonEmpty(body.Alias, chat.Alias))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	// build message list; resubmit: truncate from the referenced message
	msgs := chat.Messages
	if body.MessageID != "" {
		idx := -1
		for i, m := range msgs {
			if m.ID == body.MessageID {
				idx = i
				break
			}
		}
		if idx >= 0 {
			msgs = msgs[:idx]
		}
	}
	userMsg := ChatMessage{ID: newID(), Role: "user", Content: body.Content, Ts: nowMs()}
	msgs = append(msgs, userMsg)

	// assemble LLM messages with original note context always included
	var lm []llmMessage
	sys := "你是一个乐于助人的 AI 助手。"
	if strings.TrimSpace(chat.Context) != "" {
		sys += "\n\n以下是用户最初选中的笔记内容（作为整个对话的固定上下文，每轮都必须参考）：\n" + chat.Context
	}
	lm = append(lm, llmMessage{Role: "system", Content: sys})
	for _, m := range msgs {
		lm = append(lm, llmMessage{Role: m.Role, Content: m.Content})
	}

	// persist the user message first
	finalMsgs := msgs
	chat, _ = s.store.UpdateChat(chatID, func(ch *Chat) {
		ch.Messages = append([]ChatMessage{}, finalMsgs...)
	})

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fl, _ := w.(http.Flusher)

	emit := func(c ChatChunk) {
		b, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	emit(ChatChunk{Type: "user", Text: userMsg.ID})

	var full strings.Builder
	usage := ChatChunk{}
	streamErr := StreamChat(p, lm, func(c ChatChunk) {
		if c.Type == "delta" {
			full.WriteString(c.Text)
		}
		if c.Type == "done" {
			usage = c
		}
		emit(c)
	})
	if streamErr != nil {
		emit(ChatChunk{Type: "error", Error: streamErr.Error()})
		return
	}

	pt, ct := usage.PromptTokens, usage.CompletionTokens
	if pt == 0 && ct == 0 {
		var in strings.Builder
		for _, m := range lm {
			in.WriteString(m.Content)
		}
		pt = estimateTokens(in.String())
		ct = estimateTokens(full.String())
	}
	asst := ChatMessage{ID: newID(), Role: "assistant", Content: full.String(), Ts: nowMs(), PromptTokens: pt, CompletionTokens: ct}
	chat, _ = s.store.UpdateChat(chatID, func(ch *Chat) {
		ch.Messages = append(ch.Messages, asst)
	})
	b, _ := json.Marshal(map[string]any{"type": "saved", "id": asst.ID, "promptTokens": pt, "completionTokens": ct})
	fmt.Fprintf(w, "data: %s\n\n", b)

	// 首次回答后：用 AI 给「chat N」占位标题换成一个简短的主题标题
	if chat.TitleAuto {
		newTitle := s.summarizeChatTitle(p, body.Content, full.String())
		if newTitle != "" {
			chat, _ = s.store.UpdateChat(chatID, func(ch *Chat) {
				ch.Title = newTitle
				ch.TitleAuto = false
			})
			tb, _ := json.Marshal(map[string]any{"type": "title", "title": newTitle})
			fmt.Fprintf(w, "data: %s\n\n", tb)
		}
	}
	if fl != nil {
		fl.Flush()
	}
}

// summarizeChatTitle 让模型用一句话概括这轮对话的主题，作为对话标题。
func (s *Server) summarizeChatTitle(p LLMProfile, question, answer string) string {
	msgs := []llmMessage{
		{Role: "system", Content: "你是标题生成助手。根据用户与 AI 的一轮对话，输出一个不超过 12 个汉字的简短标题，用来标识这次讨论的主题。只输出标题本身，不要引号、不要解释、不要标点结尾。"},
		{Role: "user", Content: "用户提问：" + truncate(question, 1500) + "\n\nAI 回答：" + truncate(answer, 1500)},
	}
	reply, err := SimpleChat(p, msgs)
	if err != nil {
		return ""
	}
	t := strings.TrimSpace(reply)
	t = strings.Trim(t, "\"'《》「」【】。. \n\t")
	t = strings.SplitN(t, "\n", 2)[0]
	if len([]rune(t)) > 24 {
		t = string([]rune(t)[:24])
	}
	if t == "" {
		return ""
	}
	return t
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request, chatID, msgID string) {
	truncateFrom := r.URL.Query().Get("from") == "1"
	c, ok := s.store.UpdateChat(chatID, func(ch *Chat) {
		idx := -1
		for i, m := range ch.Messages {
			if m.ID == msgID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		if truncateFrom {
			// rollback: drop this message and everything after it
			ch.Messages = ch.Messages[:idx]
			return
		}
		end := idx + 1
		// deleting a user turn also removes its assistant reply
		if ch.Messages[idx].Role == "user" && end < len(ch.Messages) && ch.Messages[end].Role == "assistant" {
			end++
		}
		ch.Messages = append(ch.Messages[:idx], ch.Messages[end:]...)
	})
	if !ok {
		writeErr(w, 404, "对话不存在")
		return
	}
	writeJSON(w, 200, c)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---- export ----

var fnameSanitizer = strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	// 支持按选中导出：?type=note|arch&ids=a,b,c ；不带参数则导出全部
	kind := r.URL.Query().Get("type")
	rawIDs := r.URL.Query().Get("ids")
	sel := map[string]bool{}
	for _, v := range strings.Split(rawIDs, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			sel[v] = true
		}
	}
	only := len(sel) > 0

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=noteharness-export-%s.zip", time.Now().Format("20060102-150405")))
	zw := zip.NewWriter(w)
	add := func(path, content string) {
		f, err := zw.Create(path)
		if err != nil {
			return
		}
		_, _ = f.Write([]byte(content))
	}
	if kind != "arch" {
		for _, n := range s.store.ListNotes() {
			if only && !sel[n.ID] {
				continue
			}
			name := fnameSanitizer.Replace(n.Name)
			add(fmt.Sprintf("notes/%s-%s.md", time.UnixMilli(n.CreatedAt).Format("20060102"), name), n.Content)
		}
	}
	if kind != "note" {
		a, _ := s.store.ListArchive()
		for _, n := range a {
			if only && !sel[n.ID] {
				continue
			}
			name := fnameSanitizer.Replace(n.Name)
			header := ""
			if len(n.Tags) > 0 {
				header = "tags: " + strings.Join(n.Tags, ", ") + "\n\n"
			}
			add(fmt.Sprintf("archive/%s/%s.md", n.Date, name), header+n.Content)
		}
	}
	_ = zw.Close()
}
