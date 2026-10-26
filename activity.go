package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const activityFileName = "activity.json"

// maxActivityEvents caps the persisted event log so the file stays small.
const maxActivityEvents = 100

// activityStore records what the device has done — syncs, downloads, reading
// progress — plus library changes made through the web UI, so the UI can show
// when the Kobo last synced and which books it has picked up. It persists to
// the cache dir when one is configured and is safe for concurrent use.
type activityStore struct {
	path string // empty = in-memory only

	mu   sync.Mutex
	data activityData
}

type activityData struct {
	LastSync *syncRecord             `json:"lastSync,omitempty"`
	Books    map[string]bookActivity `json:"books"`
	Events   []activityEvent         `json:"events"` // newest first
}

type syncRecord struct {
	At        time.Time `json:"at"`
	UserAgent string    `json:"userAgent"`
	Books     int       `json:"books"`
}

// bookActivity is the per-book device state, keyed by book UUID.
type bookActivity struct {
	LastDownloaded *time.Time       `json:"lastDownloaded,omitempty"`
	Progress       *readingProgress `json:"progress,omitempty"`
}

// readingProgress is the subset of a Kobo ReadingState the UI cares about.
type readingProgress struct {
	Percent              float64   `json:"percent"`
	Status               string    `json:"status"` // ReadyToRead, Reading, Finished
	SpentReadingMinutes  int       `json:"spentReadingMinutes"`
	RemainingTimeMinutes int       `json:"remainingTimeMinutes"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type activityEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // sync, download, status, upload, delete
	BookID string    `json:"bookId,omitempty"`
	Title  string    `json:"title,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

func newActivityStore(dir string) *activityStore {
	a := &activityStore{data: activityData{Books: map[string]bookActivity{}}}
	if dir == "" {
		return a
	}
	a.path = filepath.Join(dir, activityFileName)
	data, err := os.ReadFile(a.path)
	if err != nil {
		return a
	}
	var d activityData
	if err := json.Unmarshal(data, &d); err != nil {
		slog.Warn("activity: ignoring corrupt activity file", "err", err)
		return a
	}
	if d.Books == nil {
		d.Books = map[string]bookActivity{}
	}
	a.data = d
	return a
}

// save atomically writes the activity file. Caller must hold a.mu.
func (a *activityStore) save() {
	if a.path == "" {
		return
	}
	data, err := json.MarshalIndent(a.data, "", "  ")
	if err != nil {
		slog.Warn("activity: marshal failed", "err", err)
		return
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Warn("activity: write failed", "err", err)
		return
	}
	if err := os.Rename(tmp, a.path); err != nil {
		slog.Warn("activity: rename failed", "err", err)
	}
}

// addEvent prepends e to the event log. Caller must hold a.mu.
func (a *activityStore) addEvent(e activityEvent) {
	a.data.Events = append([]activityEvent{e}, a.data.Events...)
	if len(a.data.Events) > maxActivityEvents {
		a.data.Events = a.data.Events[:maxActivityEvents]
	}
}

func (a *activityStore) recordSync(userAgent string, books int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	a.data.LastSync = &syncRecord{At: now, UserAgent: userAgent, Books: books}
	a.addEvent(activityEvent{At: now, Kind: "sync", Detail: pluralBooks(books)})
	a.save()
}

func (a *activityStore) recordDownload(id, title string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	ba := a.data.Books[id]
	ba.LastDownloaded = &now
	a.data.Books[id] = ba
	a.addEvent(activityEvent{At: now, Kind: "download", BookID: id, Title: title})
	a.save()
}

// recordProgress stores the device's latest reading state for a book. Only a
// change of reading status (started, finished) is added to the event log;
// plain page progress would flood it. The UI resolves the title from BookID.
func (a *activityStore) recordProgress(id string, p readingProgress) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p.UpdatedAt = time.Now().UTC()
	ba := a.data.Books[id]
	prevStatus := ""
	if ba.Progress != nil {
		prevStatus = ba.Progress.Status
	}
	ba.Progress = &p
	a.data.Books[id] = ba
	if p.Status != "" && p.Status != prevStatus && p.Status != "ReadyToRead" {
		a.addEvent(activityEvent{At: p.UpdatedAt, Kind: "status", BookID: id, Detail: p.Status})
	}
	a.save()
}

func (a *activityStore) recordUpload(id, title string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addEvent(activityEvent{At: time.Now().UTC(), Kind: "upload", BookID: id, Title: title})
	a.save()
}

func (a *activityStore) recordDelete(id, title string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.data.Books, id)
	a.addEvent(activityEvent{At: time.Now().UTC(), Kind: "delete", BookID: id, Title: title})
	a.save()
}

func (a *activityStore) lastSync() *syncRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.data.LastSync == nil {
		return nil
	}
	ls := *a.data.LastSync
	return &ls
}

func (a *activityStore) book(id string) bookActivity {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.data.Books[id]
}

func (a *activityStore) events() []activityEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]activityEvent{}, a.data.Events...)
}

func pluralBooks(n int) string {
	if n == 1 {
		return "1 book"
	}
	return strconv.Itoa(n) + " books"
}
