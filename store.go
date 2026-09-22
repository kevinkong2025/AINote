package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type LLMProfile struct {
	Alias      string `json:"alias"`
	BaseURL    string `json:"baseUrl"`
	Model      string `json:"model"`
	APIKey     string `json:"apiKey"`
	MaxContext int    `json:"maxContext"` // 0 => 256k
	Format     string `json:"format"`     // "openai" | "anthropic"
}

type PresetOption struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

type Config struct {
	Profiles    []LLMProfile   `json:"profiles"`
	ActiveAlias string         `json:"activeAlias"`
	Presets     []PresetOption `json:"presets"`
	Theme       string         `json:"theme"` // "light" | "dark"
}

type Note struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Kind      string `json:"kind"` // "draft" | "summary"
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type ArchivedNote struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Content    string   `json:"content"`
	Tags       []string `json:"tags"`
	Date       string   `json:"date"` // YYYY-MM-DD
	PointID    string   `json:"pointId"`
	PointName  string   `json:"pointName"`
	ArchivedAt int64    `json:"archivedAt"`
}

type ArchivePoint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Date string `json:"date"` // YYYY-MM-DD
}

type ChatMessage struct {
	ID               string `json:"id"`
	Role             string `json:"role"` // "user" | "assistant"
	Content          string `json:"content"`
	Ts               int64  `json:"ts"`
	PromptTokens     int    `json:"promptTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens,omitempty"`
}

type NoteRef struct {
	Type string `json:"type"` // "note" | "arch"
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Chat struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	TitleAuto bool          `json:"titleAuto"` // true => 仍是 chat N 占位标题，首次回答后由 AI 总结替换
	Context   string        `json:"context"`
	NoteNames []string      `json:"noteNames"`
	NoteIDs   []NoteRef     `json:"noteIds"`
	Alias     string        `json:"alias"`
	Messages  []ChatMessage `json:"messages"`
	CreatedAt int64         `json:"createdAt"`
	UpdatedAt int64         `json:"updatedAt"`
}

type storeData struct {
	Notes    []Note         `json:"notes"`
	Archive  []ArchivedNote `json:"archive"`
	Points   []ArchivePoint `json:"points"`
	Chats    []Chat         `json:"chats"`
	Config   Config         `json:"config"`
	DraftSeq int            `json:"draftSeq"`
	ChatSeq  int            `json:"chatSeq"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data storeData
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowMs() int64 { return time.Now().UnixMilli() }

func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	s.data.Config.Theme = "light"
	s.data.Config.Presets = []PresetOption{
		{Label: "总结这篇笔记", Prompt: "请总结这篇笔记的核心内容、技术点和难点，用要点列出。"},
		{Label: "提炼技术难点", Prompt: "请从笔记中提炼涉及的技术难点和对应的解决方案，整理成表格。"},
		{Label: "生成简历素材", Prompt: "请基于这些笔记内容，提炼可用于简历的项目经历描述（STAR 法则）。"},
		{Label: "周/年度总结", Prompt: "请将这些笔记按时间线整理成工作总结，突出成果与收获。"},
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &s.data); err != nil {
			// 数据文件损坏时不要直接崩掉（GUI 版看不到控制台），备份后以空库启动
			_ = os.Rename(path, path+".corrupt-"+time.Now().Format("20060102-150405"))
		}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---- notes ----

func (s *Store) ListNotes() []Note {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Note, len(s.data.Notes))
	copy(out, s.data.Notes)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func (s *Store) CreateNote(kind string) Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" {
		kind = "draft"
	}
	name := ""
	if kind == "draft" {
		s.data.DraftSeq++
		name = "draft " + itoa(s.data.DraftSeq)
	}
	n := Note{ID: newID(), Name: name, Kind: kind, CreatedAt: nowMs(), UpdatedAt: nowMs()}
	s.data.Notes = append(s.data.Notes, n)
	_ = s.saveLocked()
	return n
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}

func (s *Store) AddNote(n Note) Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.ID == "" {
		n.ID = newID()
	}
	if n.Kind == "" {
		n.Kind = "summary"
	}
	n.CreatedAt = nowMs()
	n.UpdatedAt = nowMs()
	s.data.Notes = append(s.data.Notes, n)
	_ = s.saveLocked()
	return n
}

func (s *Store) GetNote(id string) (Note, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.data.Notes {
		if n.ID == id {
			return n, true
		}
	}
	return Note{}, false
}

func (s *Store) UpdateNote(id, name, content string) (Note, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data.Notes {
		if n.ID == id {
			if name != "" {
				s.data.Notes[i].Name = name
			}
			s.data.Notes[i].Content = content
			s.data.Notes[i].UpdatedAt = nowMs()
			_ = s.saveLocked()
			return s.data.Notes[i], true
		}
	}
	return Note{}, false
}

func (s *Store) DeleteNote(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data.Notes {
		if n.ID == id {
			s.data.Notes = append(s.data.Notes[:i], s.data.Notes[i+1:]...)
			_ = s.saveLocked()
			return true
		}
	}
	return false
}

// ---- archive ----

func (s *Store) ArchiveNotes(noteIDs []string, pointName, date, tag string) (ArchivePoint, []ArchivedNote) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if pointName == "" {
		pointName = "Summary " + time.Now().Format("2006-01-02 15:04:05")
	}
	pt := ArchivePoint{ID: newID(), Name: pointName, Date: date}
	s.data.Points = append(s.data.Points, pt)
	var added []ArchivedNote
	for _, id := range noteIDs {
		for _, n := range s.data.Notes {
			if n.ID == id {
				tags := []string{}
				if tag != "" {
					tags = append(tags, tag)
				}
				an := ArchivedNote{
					ID:         newID(),
					Name:       n.Name,
					Content:    n.Content,
					Tags:       tags,
					Date:       date,
					PointID:    pt.ID,
					PointName:  pt.Name,
					ArchivedAt: nowMs(),
				}
				s.data.Archive = append(s.data.Archive, an)
				added = append(added, an)
			}
		}
	}
	_ = s.saveLocked()
	return pt, added
}

func (s *Store) ListArchive() ([]ArchivedNote, []ArchivePoint) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := make([]ArchivedNote, len(s.data.Archive))
	copy(a, s.data.Archive)
	p := make([]ArchivePoint, len(s.data.Points))
	copy(p, s.data.Points)
	return a, p
}

func (s *Store) GetArchived(id string) (ArchivedNote, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.data.Archive {
		if n.ID == id {
			return n, true
		}
	}
	return ArchivedNote{}, false
}

func (s *Store) UpdateArchived(id, name, content string) (ArchivedNote, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data.Archive {
		if n.ID == id {
			if name != "" {
				s.data.Archive[i].Name = name
			}
			s.data.Archive[i].Content = content
			_ = s.saveLocked()
			return s.data.Archive[i], true
		}
	}
	return ArchivedNote{}, false
}

func (s *Store) DeleteArchived(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data.Archive {
		if n.ID == id {
			s.data.Archive = append(s.data.Archive[:i], s.data.Archive[i+1:]...)
			_ = s.saveLocked()
			return true
		}
	}
	return false
}

func (s *Store) TagArchived(ids []string, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for i := range s.data.Archive {
		if set[s.data.Archive[i].ID] {
			found := false
			for _, t := range s.data.Archive[i].Tags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				s.data.Archive[i].Tags = append(s.data.Archive[i].Tags, tag)
			}
		}
	}
	_ = s.saveLocked()
}

func (s *Store) RemoveTag(ids []string, tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for i := range s.data.Archive {
		if set[s.data.Archive[i].ID] {
			var nt []string
			for _, t := range s.data.Archive[i].Tags {
				if t != tag {
					nt = append(nt, t)
				}
			}
			s.data.Archive[i].Tags = nt
		}
	}
	_ = s.saveLocked()
}

// ---- config ----

// normalizeConfig 保证切片字段永不为 null：Go 的 nil slice 会序列化成 null，
// 前端 for...of null 会直接抛异常（曾导致设置页渲染失败）。
func normalizeConfig(c *Config) {
	if c.Profiles == nil {
		c.Profiles = []LLMProfile{}
	}
	if c.Presets == nil {
		c.Presets = []PresetOption{}
	}
	if c.Theme == "" {
		c.Theme = "light"
	}
}

func (s *Store) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.data.Config
	normalizeConfig(&c)
	return c
}

func (s *Store) SaveConfig(fn func(*Config)) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data.Config)
	normalizeConfig(&s.data.Config)
	_ = s.saveLocked()
	return s.data.Config
}

// ---- chats ----

func (s *Store) ListChats() []Chat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Chat, len(s.data.Chats))
	copy(out, s.data.Chats)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (s *Store) AddChat(c Chat) Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = newID()
	}
	if c.Messages == nil {
		c.Messages = []ChatMessage{}
	}
	if c.NoteNames == nil {
		c.NoteNames = []string{}
	}
	if c.NoteIDs == nil {
		c.NoteIDs = []NoteRef{}
	}
	if strings.TrimSpace(c.Title) == "" {
		s.data.ChatSeq++
		c.Title = "chat " + itoa(s.data.ChatSeq)
		c.TitleAuto = true
	}
	c.CreatedAt = nowMs()
	c.UpdatedAt = nowMs()
	s.data.Chats = append(s.data.Chats, c)
	_ = s.saveLocked()
	return c
}

func (s *Store) GetChat(id string) (Chat, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.data.Chats {
		if c.ID == id {
			return c, true
		}
	}
	return Chat{}, false
}

func (s *Store) UpdateChat(id string, fn func(*Chat)) (Chat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Chats {
		if s.data.Chats[i].ID == id {
			fn(&s.data.Chats[i])
			s.data.Chats[i].UpdatedAt = nowMs()
			_ = s.saveLocked()
			return s.data.Chats[i], true
		}
	}
	return Chat{}, false
}

func (s *Store) DeleteChat(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.data.Chats {
		if c.ID == id {
			s.data.Chats = append(s.data.Chats[:i], s.data.Chats[i+1:]...)
			_ = s.saveLocked()
			return true
		}
	}
	return false
}
