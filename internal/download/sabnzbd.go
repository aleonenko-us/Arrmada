package download

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/pathguard"
)

// SABnzbd drives SABnzbd's API: every call is a POST to {url}/api with the API key in the
// form body, so the key never sits in a URL, a proxy log or an error message. The key is the
// client's Password (never marshalled) and must be the full API key: the NZB key can add
// jobs but can't read the queue, which Arrmada needs.
//
// Only Arrmada's own categories are read and acted on. A user's own SABnzbd has jobs of
// its own, and a command for one of those must never come from here.
type SABnzbd struct {
	http *http.Client
	// add is for uploads: an NZB can run to tens of megabytes on a slow link.
	add *http.Client

	mu       sync.RWMutex
	movieCat string                            // the configured movie category ("" = default)
	folders  func(ctx context.Context) PathEnv // Arrmada's folders, for the leftover-folder guard
}

// NewSABnzbd creates the downloader with Arrmada's default categories and no folders
// (SetFolders): until it has them, removing a finished job leaves its folder in place.
func NewSABnzbd() *SABnzbd {
	return &SABnzbd{
		http: &http.Client{Timeout: 30 * time.Second},
		add:  &http.Client{Timeout: 3 * time.Minute},
	}
}

// SetMovieCategory sets the movie category (ARRMADA_DOWNLOAD_CATEGORY); the others are fixed.
func (s *SABnzbd) SetMovieCategory(movie string) {
	s.mu.Lock()
	s.movieCat = movie
	s.mu.Unlock()
}

// SetFolders wires where Arrmada keeps things, read on every use (the folders are live).
func (s *SABnzbd) SetFolders(fn func(ctx context.Context) PathEnv) {
	s.mu.Lock()
	s.folders = fn
	s.mu.Unlock()
}

// categories is Arrmada's categories, in the order they're shown.
func (s *SABnzbd) categories() []sabCategory {
	s.mu.RLock()
	c := FixedCategories(s.movieCat)
	s.mu.RUnlock()
	return []sabCategory{{c.Movies, "movies"}, {c.TV, "tv"}, {c.Books, "books"}, {c.Music, "music"}}
}

type sabCategory struct{ name, kind string }

// ours reports whether cat is one of Arrmada's categories. SABnzbd keeps category names
// lowercase, and so does Arrmada.
func (s *SABnzbd) ours(cat string) bool {
	for _, c := range s.categories() {
		if strings.EqualFold(c.name, cat) {
			return true
		}
	}
	return false
}

func (s *SABnzbd) catList() string {
	cats := s.categories()
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = c.name
	}
	return strings.Join(names, ",")
}

// --- transport -----------------------------------------------------------------------

// errSABKey: SABnzbd refused the API key (wrong, missing, or the NZB key).
var errSABKey = errors.New("SABnzbd refused the API key. Use the full API key from SABnzbd → Config → General (the NZB key can't read the queue)")

// sabHostError is SABnzbd's "Hostname verification failed": it only answers to the names
// in its host_whitelist, and the address Arrmada uses isn't one of them.
func sabHostError(dc Client) error {
	host := dc.URL
	if u, err := url.Parse(dc.URL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return fmt.Errorf("SABnzbd refused the address Arrmada uses for it (hostname verification). Add %s to SABnzbd → Config → Special → host_whitelist", host)
}

// sabURL is the API endpoint, without any user info a pasted address might carry.
func sabURL(dc Client) string {
	return strings.TrimRight(dc.URL, "/") + "/api"
}

// sabMaxBody caps an API answer. A long history is the biggest thing SABnzbd sends.
const sabMaxBody = 32 << 20

// scrub takes the API key out of text that came back from SABnzbd or the network, in case
// anything ever echoes it — nothing Arrmada shows or logs may carry it.
func scrub(dc Client, s string) string {
	if k := strings.TrimSpace(dc.Password); len(k) >= 4 {
		s = strings.ReplaceAll(s, k, "[api key]")
		s = strings.ReplaceAll(s, url.QueryEscape(k), "[api key]")
	}
	return s
}

// scrubErr is scrub for an error, keeping NeverReached.
func scrubErr(dc Client, err error) error {
	if err == nil {
		return nil
	}
	msg := scrub(dc, err.Error())
	if NeverReached(err) {
		return notSent(errors.New(msg))
	}
	return errors.New(msg)
}

// call posts one API command and returns the answer's body. params must not carry the key;
// it's added here.
func (s *SABnzbd) call(ctx context.Context, dc Client, mode string, params url.Values) ([]byte, error) {
	form := url.Values{}
	for k, v := range params {
		form[k] = v
	}
	form.Set("mode", mode)
	form.Set("output", "json")
	form.Set("apikey", dc.Password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sabURL(dc), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, scrubErr(dc, fmt.Errorf("sabnzbd: bad address: %w", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(dc, s.http, req)
}

func (s *SABnzbd) do(dc Client, hc *http.Client, req *http.Request) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		err = fmt.Errorf("couldn't reach SABnzbd: %w", err)
		if dialFailed(err) {
			err = notSent(err)
		}
		return nil, scrubErr(dc, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, sabMaxBody))
	if err != nil {
		return nil, scrubErr(dc, fmt.Errorf("sabnzbd: reading the answer: %w", err))
	}
	// SABnzbd's refusals are plain text, not JSON (so a job named after one can't fake it).
	text := string(body)
	plain := !strings.HasPrefix(strings.TrimSpace(text), "{")
	switch {
	case plain && strings.Contains(text, "Hostname verification failed"):
		// Refused before it looked at the command: nothing was done.
		return nil, notSent(sabHostError(dc))
	case plain && (strings.Contains(text, "API Key Incorrect") || strings.Contains(text, "API Key Required")):
		return nil, notSent(errSABKey)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, notSent(fmt.Errorf("SABnzbd refused the request (HTTP %d). Check the API key and that the address is SABnzbd's", resp.StatusCode))
	case resp.StatusCode/100 != 2:
		return nil, fmt.Errorf("sabnzbd: HTTP %d", resp.StatusCode)
	}
	// {"status": false, "error": "…"} is a refused command.
	var st struct {
		Status *bool  `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(body, &st) == nil && st.Status != nil && !*st.Status && st.Error != "" {
		return nil, fmt.Errorf("sabnzbd: %s", scrub(dc, st.Error))
	}
	return body, nil
}

// callStatus is call for a command that answers {"status": true|false}.
func (s *SABnzbd) callStatus(ctx context.Context, dc Client, mode string, params url.Values) error {
	body, err := s.call(ctx, dc, mode, params)
	if err != nil {
		return err
	}
	var st struct {
		Status bool `json:"status"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return fmt.Errorf("sabnzbd: %s: unreadable answer", mode)
	}
	if !st.Status {
		return fmt.Errorf("sabnzbd: %s was refused", mode)
	}
	return nil
}

func decode[T any](dc Client, body []byte, what string) (T, error) {
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("sabnzbd: %s: unreadable answer: %s", what, scrub(dc, err.Error()))
	}
	return v, nil
}

// --- connection ------------------------------------------------------------------------

// Version is SABnzbd's version. It needs no key, so it only proves something answers.
func (s *SABnzbd) Version(ctx context.Context, dc Client) (string, error) {
	body, err := s.call(ctx, dc, "version", nil)
	if err != nil {
		return "", err
	}
	v, err := decode[struct {
		Version string `json:"version"`
	}](dc, body, "version")
	if err != nil {
		return "", errors.New("the address answered, but not as SABnzbd. Check the URL (include any URL base, e.g. http://host:8080/sabnzbd)")
	}
	if v.Version == "" {
		return "", errors.New("the address answered, but not as SABnzbd. Check the URL")
	}
	return v.Version, nil
}

// Categories is the categories SABnzbd has. Reading them needs the full key.
func (s *SABnzbd) Categories(ctx context.Context, dc Client) ([]string, error) {
	body, err := s.call(ctx, dc, "get_cats", nil)
	if err != nil {
		return nil, err
	}
	v, err := decode[struct {
		Categories []string `json:"categories"`
	}](dc, body, "categories")
	return v.Categories, err
}

// sabStatus is the slice of fullstatus Arrmada reads. The full answer carries the API key,
// so it is decoded into this and never kept or logged whole.
type sabStatus struct {
	Status struct {
		Version     string `json:"version"`
		CompleteDir string `json:"completedir"`
		DownloadDir string `json:"downloaddir"`
		Servers     []struct {
			Name     string `json:"servername"`
			Error    string `json:"servererror"`
			Optional flexInt `json:"serveroptional"`
			Total    flexInt `json:"servertotalconn"`
		} `json:"servers"`
	} `json:"status"`
}

func (s *SABnzbd) fullStatus(ctx context.Context, dc Client) (sabStatus, error) {
	body, err := s.call(ctx, dc, "fullstatus", url.Values{"skip_dashboard": {"1"}})
	if err != nil {
		return sabStatus{}, err
	}
	return decode[sabStatus](dc, body, "status")
}

// Test checks, in order, that the address is SABnzbd, that the key is the full API key,
// and that its status can be read. Each failure says plainly what to fix.
func (s *SABnzbd) Test(ctx context.Context, dc Client) error {
	if _, err := s.Version(ctx, dc); err != nil {
		return err
	}
	if _, err := s.Categories(ctx, dc); err != nil {
		return err
	}
	_, err := s.fullStatus(ctx, dc)
	return err
}

// Ping is the minute-by-minute health check: one call that needs the key.
func (s *SABnzbd) Ping(ctx context.Context, dc Client) error {
	_, err := s.Categories(ctx, dc)
	return err
}

// --- adding ---------------------------------------------------------------------------

// ErrNZBRequired: SABnzbd is only ever handed the NZB itself. Passing it a link would put
// the indexer's API key into SABnzbd's history; Arrmada fetches NZBs itself.
var ErrNZBRequired = errors.New("sabnzbd: Arrmada sends SABnzbd the NZB file itself, not a link")

// Add hands SABnzbd an NZB (see AddKeyed).
func (s *SABnzbd) Add(ctx context.Context, dc Client, req AddRequest) error {
	_, err := s.AddKeyed(ctx, dc, req)
	return err
}

// AddKeyed uploads the NZB (addfile) under the release title and Arrmada's category, at the
// category's priority, with repair + unpack + delete (pp=3), and the password the NZB carries.
// It answers with the job's download key.
//
// A category SABnzbd doesn't have is refused here: SABnzbd would silently file the job
// under its default (*) category, where no importer ever looks.
func (s *SABnzbd) AddKeyed(ctx context.Context, dc Client, req AddRequest) (string, error) {
	if len(req.File) == 0 {
		return "", ErrNZBRequired
	}
	if strings.TrimSpace(req.Category) == "" {
		return "", errors.New("sabnzbd: no category for this download")
	}
	cats, err := s.Categories(ctx, dc)
	if err != nil {
		return "", err
	}
	if !hasCategory(cats, req.Category) {
		return "", missingCategoryError(dc, req.Category)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	name := req.Filename
	if name == "" {
		name = "arrmada.nzb"
	}
	part, err := w.CreateFormFile("nzbfile", name)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(req.File); err != nil {
		return "", err
	}
	fields := [][2]string{
		{"mode", "addfile"}, {"output", "json"}, {"apikey", dc.Password},
		{"cat", req.Category},
		{"pp", "3"},
	}
	if req.Name != "" {
		fields = append(fields, [2]string{"nzbname", req.Name})
	}
	// -100 is "the category's priority"; -2 adds it paused.
	if req.Paused {
		fields = append(fields, [2]string{"priority", "-2"})
	} else {
		fields = append(fields, [2]string{"priority", "-100"})
	}
	if pw := nzbPassword(req.File); pw != "" {
		fields = append(fields, [2]string{"password", pw})
	}
	for _, f := range fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sabURL(dc), &buf)
	if err != nil {
		return "", scrubErr(dc, fmt.Errorf("sabnzbd: bad address: %w", err))
	}
	httpReq.Header.Set("Content-Type", w.FormDataContentType())
	body, err := s.do(dc, s.add, httpReq)
	if err != nil {
		return "", err
	}
	v, err := decode[struct {
		Status bool     `json:"status"`
		IDs    []string `json:"nzo_ids"`
	}](dc, body, "add")
	if err != nil {
		return "", err
	}
	if !v.Status || len(v.IDs) == 0 {
		return "", errors.New("sabnzbd: it refused the NZB (damaged, empty, or not an NZB)")
	}
	return UsenetKey(v.IDs[0]), nil
}

func hasCategory(cats []string, cat string) bool {
	for _, c := range cats {
		if strings.EqualFold(c, cat) {
			return true
		}
	}
	return false
}

func missingCategoryError(dc Client, cat string) error {
	return fmt.Errorf("%s has no %q category, so SABnzbd would file the download where Arrmada never looks. Use \"Add missing categories\" on its card in Arrmada, or add it in SABnzbd → Config → Categories", dc.Name, cat)
}

// nzbPassword is the password an NZB declares in its head (<meta type="password">), "" when
// none. Only the head is read: it comes before the first <file>.
func nzbPassword(nzb []byte) string {
	d := xml.NewDecoder(bytes.NewReader(nzb))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	inPW := false
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "file":
				return ""
			case "meta":
				for _, a := range t.Attr {
					if a.Name.Local == "type" && strings.EqualFold(a.Value, "password") {
						inPW = true
					}
				}
			}
		case xml.CharData:
			if inPW {
				return strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			if t.Name.Local == "meta" {
				inPW = false
			}
		}
	}
}

// --- reading the queue ------------------------------------------------------------------

// flexFloat reads a number SABnzbd sends either bare or as a string ("0.73", "").
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil //nolint:nilerr // an odd value reads as 0 rather than losing the whole queue
	}
	*f = flexFloat(v)
	return nil
}

// flexInt is flexFloat for whole numbers.
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	var f flexFloat
	if err := f.UnmarshalJSON(b); err != nil {
		return err
	}
	*n = flexInt(f)
	return nil
}

type sabQueue struct {
	Queue struct {
		Paused   bool          `json:"paused"`
		KBPerSec flexFloat     `json:"kbpersec"`
		Slots    []sabQueueJob `json:"slots"`
	} `json:"queue"`
}

type sabQueueJob struct {
	ID         string    `json:"nzo_id"`
	Name       string    `json:"filename"`
	Category   string    `json:"cat"`
	Status     string    `json:"status"`
	MB         flexFloat `json:"mb"`
	MBLeft     flexFloat `json:"mbleft"`
	Percentage flexFloat `json:"percentage"`
	TimeLeft   string    `json:"timeleft"`
	Labels     []string  `json:"labels"`
	TimeAdded  flexInt   `json:"time_added"`
}

type sabHistory struct {
	History struct {
		Slots []sabHistoryJob `json:"slots"`
	} `json:"history"`
}

type sabHistoryJob struct {
	ID          string  `json:"nzo_id"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Status      string  `json:"status"`
	FailMessage string  `json:"fail_message"`
	Storage     string  `json:"storage"`
	Bytes       flexInt `json:"bytes"`
	Completed   flexInt `json:"completed"`
	TimeAdded   flexInt `json:"time_added"`
}

const mib = 1 << 20

func (s *SABnzbd) queue(ctx context.Context, dc Client) (sabQueue, error) {
	body, err := s.call(ctx, dc, "queue", url.Values{"cat": {s.catList()}, "limit": {"0"}})
	if err != nil {
		return sabQueue{}, err
	}
	return decode[sabQueue](dc, body, "queue")
}

func (s *SABnzbd) history(ctx context.Context, dc Client) (sabHistory, error) {
	body, err := s.call(ctx, dc, "history", url.Values{"category": {s.catList()}, "limit": {"0"}})
	if err != nil {
		return sabHistory{}, err
	}
	return decode[sabHistory](dc, body, "history")
}

// List reads the queue and the history, both only in Arrmada's categories. A job in the
// queue is downloading (or waiting); one in the history is being unpacked, finished, or
// failed. A job keeps its key from the queue to the history.
func (s *SABnzbd) List(ctx context.Context, dc Client) ([]Item, error) {
	q, err := s.queue(ctx, dc)
	if err != nil {
		return nil, err
	}
	h, err := s.history(ctx, dc)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(q.Queue.Slots)+len(h.History.Slots))
	speedGiven := false
	for _, j := range q.Queue.Slots {
		if !s.ours(j.Category) {
			continue
		}
		it, ok := queueItem(j, q.Queue.Paused)
		if !ok {
			continue
		}
		// SABnzbd reports one speed for the whole queue and fetches one job at a time;
		// the job it's on is the one moving.
		if !speedGiven && it.State == "downloading" && j.Status == "Downloading" {
			it.DownSpeed = int64(float64(q.Queue.KBPerSec) * 1024)
			speedGiven = true
		}
		items = append(items, it)
	}
	for _, j := range h.History.Slots {
		if !s.ours(j.Category) {
			continue
		}
		if it, ok := historyItem(j); ok {
			items = append(items, it)
		}
	}
	return items, nil
}

// queueItem maps a queue job. RawState keeps SABnzbd's word; Phase reads it.
func queueItem(j sabQueueJob, queuePaused bool) (Item, bool) {
	key := UsenetKey(j.ID)
	if key == "" {
		return Item{}, false
	}
	size := int64(float64(j.MB) * mib)
	left := int64(float64(j.MBLeft) * mib)
	if left > size {
		left = size
	}
	it := Item{
		Hash:            key,
		Name:            j.Name,
		State:           "downloading",
		Progress:        clamp01(float64(j.Percentage) / 100),
		SizeBytes:       size,
		TotalSizeBytes:  size,
		RemainingBytes:  left,
		DownloadedBytes: size - left,
		ETASeconds:      parseSABDuration(j.TimeLeft),
		Category:        j.Category,
		RawState:        j.Status,
		AddedOn:         int64(j.TimeAdded),
		Transport:       TransportUsenet,
		Labels:          j.Labels,
		Availability:    -1,
	}
	// Nothing finishes in the queue (Complete reads only a history Completed): a job at
	// 100% here is about to move to the history for unpacking.
	if queuePaused || j.Status == "Paused" {
		it.State = "paused"
	}
	return it, true
}

// historyItem maps a history job: Completed is the only finished state; Failed is an error
// with SABnzbd's reason; everything else is post-processing (verify, repair, unpack, move).
func historyItem(j sabHistoryJob) (Item, bool) {
	key := UsenetKey(j.ID)
	if key == "" {
		return Item{}, false
	}
	size := int64(j.Bytes)
	it := Item{
		Hash:            key,
		Name:            j.Name,
		SizeBytes:       size,
		TotalSizeBytes:  size,
		DownloadedBytes: size,
		Category:        j.Category,
		RawState:        j.Status,
		Transport:       TransportUsenet,
		Availability:    -1,
		AddedOn:         int64(j.TimeAdded),
		LastActivity:    int64(j.Completed),
	}
	if it.AddedOn == 0 {
		it.AddedOn = int64(j.Completed) // an older history entry without it
	}
	switch j.Status {
	case "Completed":
		it.State = StateCompleted
		it.Progress = 1
		it.ContentPath = j.Storage
	case "Failed":
		it.State = "error"
		it.FailMessage = j.FailMessage
		if it.FailMessage == "" {
			it.FailMessage = "SABnzbd marked the download failed"
		}
		it.RemainingBytes = size
		it.DownloadedBytes = 0
	default:
		it.State = PhaseUnpacking
		it.Progress = 1
	}
	return it, true
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

// parseSABDuration reads SABnzbd's time left ("0:12:05", "1:02:03:04" with days); 0 for
// anything else.
func parseSABDuration(s string) int64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 4 {
		return 0
	}
	mult := []int64{1, 60, 3600, 86400}
	var total int64
	for i := range parts {
		n, err := strconv.ParseInt(parts[len(parts)-1-i], 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		total += n * mult[i]
	}
	return total
}

// --- commands -------------------------------------------------------------------------

// sabJob is one of Arrmada's jobs, found by key, with the id SABnzbd knows it by.
type sabJob struct {
	id      string // SABnzbd's own id, in its own case: it matches ids case-sensitively
	inQueue bool
	status  string
	storage string
}

// find looks a key up among Arrmada's jobs. A key is lowercase; SABnzbd 4.x ids aren't,
// and a command with the lowercased id is answered "ok" while doing nothing. So the real
// id always comes from a fresh read. false when no such job (any more).
func (s *SABnzbd) find(ctx context.Context, dc Client, key string) (sabJob, bool, error) {
	if KeyTransport(key) != TransportUsenet {
		return sabJob{}, false, fmt.Errorf("sabnzbd: %q isn't a usenet download", key)
	}
	q, err := s.queue(ctx, dc)
	if err != nil {
		return sabJob{}, false, err
	}
	for _, j := range q.Queue.Slots {
		if s.ours(j.Category) && UsenetKey(j.ID) == key {
			return sabJob{id: j.ID, inQueue: true, status: j.Status}, true, nil
		}
	}
	h, err := s.history(ctx, dc)
	if err != nil {
		return sabJob{}, false, err
	}
	for _, j := range h.History.Slots {
		if s.ours(j.Category) && UsenetKey(j.ID) == key {
			return sabJob{id: j.ID, status: j.Status, storage: j.Storage}, true, nil
		}
	}
	return sabJob{}, false, nil
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Remove takes a job out of SABnzbd. One still downloading is deleted (with its partial
// files when deleteData). A failed one is deleted from the history, its partial files with it
// when deleteData. A finished one is archived in the history (SABnzbd keeps a record) and,
// when deleteData, Arrmada deletes the folder it was unpacked into itself — but only a
// folder inside Arrmada's downloads folder or SABnzbd's completed folder, never one of those
// folders themselves, a category folder, a library or the database folder (leftoverTarget).
// A folder outside all that is refused before anything is touched. A job that is already
// gone is no error.
func (s *SABnzbd) Remove(ctx context.Context, dc Client, key string, deleteData bool) error {
	job, ok, err := s.find(ctx, dc, key)
	if err != nil || !ok {
		return err
	}
	if job.inQueue {
		return s.callStatus(ctx, dc, "queue", url.Values{"name": {"delete"}, "value": {job.id}, "del_files": {boolParam(deleteData)}})
	}
	if job.status != "Completed" {
		return s.callStatus(ctx, dc, "history", url.Values{"name": {"delete"}, "value": {job.id}, "del_files": {boolParam(deleteData)}, "archive": {"1"}})
	}
	var target string
	var guard leftoverGuard
	if deleteData && job.storage != "" {
		target, guard, err = s.leftoverTarget(ctx, dc, job.storage)
		if err != nil {
			return err
		}
	}
	if err := s.callStatus(ctx, dc, "history", url.Values{"name": {"delete"}, "value": {job.id}, "del_files": {"0"}, "archive": {"1"}}); err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	if err := deleteLeftover(target, guard); err != nil {
		return fmt.Errorf("SABnzbd archived the job, but what it left at %s couldn't be deleted: %w", target, err)
	}
	return nil
}

// deleteLeftover deletes a finished job's leftovers. SABnzbd's storage is the job's folder,
// or — for a job of one file — that file, inside the job's folder: then the file goes, and
// the folder too once it's empty and passes the same guard.
func deleteLeftover(target string, guard leftoverGuard) error {
	fi, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(target)
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	if parent, err := guard.check(filepath.Dir(target)); err == nil {
		_ = os.Remove(parent) // only ever an empty folder
	}
	return nil
}

// ErrLeftoverOutside: a finished job's folder isn't somewhere Arrmada may delete from.
var ErrLeftoverOutside = errors.New("not inside Arrmada's downloads folder or SABnzbd's completed folder")

// leftoverTarget is the folder (as Arrmada sees it) a finished job left behind, once it has
// passed every guard: strictly inside Arrmada's downloads folder or SABnzbd's completed
// folder (resolved, so a symlink or ".." can't fake it), and not one of those folders, a
// category folder, a library folder (or a folder holding one) or the database folder.
func (s *SABnzbd) leftoverTarget(ctx context.Context, dc Client, storage string) (string, leftoverGuard, error) {
	s.mu.RLock()
	folders := s.folders
	s.mu.RUnlock()
	if folders == nil {
		return "", leftoverGuard{}, fmt.Errorf("left %s in place: Arrmada doesn't know its downloads folder", storage)
	}
	env := folders(ctx)
	local := dc.PathMaps.ToLocal(storage)
	if local == "" || !filepath.IsAbs(local) {
		return "", leftoverGuard{}, fmt.Errorf("left %s in place: it isn't a full path here", storage)
	}
	st, err := s.fullStatus(ctx, dc)
	if err != nil {
		return "", leftoverGuard{}, err
	}
	complete := ""
	if st.Status.CompleteDir != "" {
		complete = dc.PathMaps.ToLocal(st.Status.CompleteDir)
	}
	catDirs, err := s.categoryFolders(ctx, dc, st.Status.CompleteDir)
	if err != nil {
		return "", leftoverGuard{}, err
	}
	g := leftoverGuard{roots: []string{env.Downloads, complete}}
	g.protected = append(g.protected, g.roots...)
	for _, f := range catDirs {
		g.protected = append(g.protected, dc.PathMaps.ToLocal(f))
	}
	g.keep = append(append(g.keep, env.Libraries...), env.DataDir)
	target, err := g.check(local)
	return target, g, err
}

// leftoverGuard says where a finished job's leftovers may be deleted. roots: deleting is
// allowed strictly inside one. protected: folders that must never be the target itself.
// keep: folders the target may be neither inside nor hold.
type leftoverGuard struct{ roots, protected, keep []string }

func (g leftoverGuard) check(local string) (string, error) {
	return guardLeftover(local, g.roots, g.protected, g.keep)
}

// guardLeftover is the guard's decision, apart so it can be tested on temp folders. It
// answers the path resolved (symlinks followed), which is what gets deleted.
func guardLeftover(local string, roots, protected, keep []string) (string, error) {
	rp, err := pathguard.Resolve(local)
	if err != nil {
		return "", fmt.Errorf("left %s in place: %w", local, err)
	}
	inside := false
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		rr, err := pathguard.Resolve(r)
		if err != nil || rr == rp {
			continue
		}
		if pathguard.Within(rp, rr) {
			inside = true
		}
	}
	if !inside {
		return "", fmt.Errorf("left %s in place: %w", local, ErrLeftoverOutside)
	}
	for _, p := range protected {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if pr, err := pathguard.Resolve(p); err == nil && pr == rp {
			return "", fmt.Errorf("left %s in place: it's a folder SABnzbd files every download into", local)
		}
	}
	for _, k := range keep {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if pathguard.Within(rp, k) || pathguard.Within(k, rp) {
			return "", fmt.Errorf("left %s in place: it's in or holds one of Arrmada's own folders", local)
		}
	}
	return rp, nil
}

// Pause holds one job, or (PauseAll) the whole queue. A job already in the history has
// nothing left to pause.
func (s *SABnzbd) Pause(ctx context.Context, dc Client, key string) error {
	if key == PauseAll {
		return s.callStatus(ctx, dc, "pause", nil)
	}
	job, ok, err := s.find(ctx, dc, key)
	if err != nil || !ok || !job.inQueue {
		return err
	}
	return s.callStatus(ctx, dc, "queue", url.Values{"name": {"pause"}, "value": {job.id}})
}

// Resume restarts one job, or (PauseAll) the whole queue.
func (s *SABnzbd) Resume(ctx context.Context, dc Client, key string) error {
	if key == PauseAll {
		return s.callStatus(ctx, dc, "resume", nil)
	}
	job, ok, err := s.find(ctx, dc, key)
	if err != nil || !ok || !job.inQueue {
		return err
	}
	return s.callStatus(ctx, dc, "queue", url.Values{"name": {"resume"}, "value": {job.id}})
}

// SetCategory re-files a job still in the queue. SABnzbd only takes a category it has
// (otherwise it files the job under * and says "ok"), and a finished job's files are
// already where its old category put them.
func (s *SABnzbd) SetCategory(ctx context.Context, dc Client, key, category string) error {
	cats, err := s.Categories(ctx, dc)
	if err != nil {
		return err
	}
	if !hasCategory(cats, category) {
		return missingCategoryError(dc, category)
	}
	job, ok, err := s.find(ctx, dc, key)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("sabnzbd: no such download")
	}
	if !job.inQueue {
		return errors.New("SABnzbd can only re-file a download that's still in its queue")
	}
	return s.callStatus(ctx, dc, "change_cat", url.Values{"value": {job.id}, "value2": {category}})
}

// ActionRetry asks SABnzbd to try a failed job again (TorrentAction).
const ActionRetry = "retry"

// TorrentAction runs a job command. SABnzbd has one: "retry" a failed job. The torrent
// commands (recheck, reannounce, priority) mean nothing to it.
func (s *SABnzbd) TorrentAction(ctx context.Context, dc Client, key, action string) error {
	if action != ActionRetry {
		return fmt.Errorf("sabnzbd: %q isn't something a usenet download can do", action)
	}
	job, ok, err := s.find(ctx, dc, key)
	if err != nil {
		return err
	}
	if !ok || job.inQueue || job.status != "Failed" {
		return errors.New("only a failed usenet download can be retried")
	}
	return s.callStatus(ctx, dc, "retry", url.Values{"value": {job.id}})
}

// --- folders, categories and health ---------------------------------------------------

// DefaultSavePath is SABnzbd's completed folder as SABnzbd names it — where finished jobs
// are unpacked, and so what Arrmada must be able to see.
func (s *SABnzbd) DefaultSavePath(ctx context.Context, dc Client) (string, error) {
	st, err := s.fullStatus(ctx, dc)
	if err != nil {
		return "", err
	}
	if st.Status.CompleteDir == "" {
		return "", errors.New("SABnzbd didn't say where it puts finished downloads")
	}
	return st.Status.CompleteDir, nil
}

type sabCatConfig struct {
	Config struct {
		Categories []struct {
			Name string `json:"name"`
			Dir  string `json:"dir"`
		} `json:"categories"`
	} `json:"config"`
}

// categoryFolders is, for each of Arrmada's categories SABnzbd has with a folder of its
// own, that folder as SABnzbd names it. A relative folder is inside the completed folder;
// a trailing "*" (no job folders) is dropped.
func (s *SABnzbd) categoryFolders(ctx context.Context, dc Client, completeDir string) (map[string]string, error) {
	body, err := s.call(ctx, dc, "get_config", url.Values{"section": {"categories"}})
	if err != nil {
		return nil, err
	}
	cfg, err := decode[sabCatConfig](dc, body, "categories")
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range cfg.Config.Categories {
		dir := strings.TrimSuffix(strings.TrimSpace(c.Dir), "*")
		dir = strings.TrimRight(dir, "/\\")
		if dir == "" || !s.ours(c.Name) {
			continue
		}
		if !isAbsRemote(dir) && completeDir != "" {
			dir = strings.TrimRight(completeDir, "/\\") + "/" + dir
		}
		out[strings.ToLower(c.Name)] = dir
	}
	return out, nil
}

// isAbsRemote reports whether a path SABnzbd gave is absolute on its side (POSIX or Windows).
func isAbsRemote(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// CategoryFolders is categoryFolders for the path check.
func (s *SABnzbd) CategoryFolders(ctx context.Context, dc Client) (map[string]string, error) {
	st, err := s.fullStatus(ctx, dc)
	if err != nil {
		return nil, err
	}
	return s.categoryFolders(ctx, dc, st.Status.CompleteDir)
}

// UsenetFacts is what a usenet client's card shows beside its connection state.
type UsenetFacts struct {
	Version string `json:"version"`
	// Categories are Arrmada's categories and whether SABnzbd has each; Missing names the
	// ones it lacks ("Add missing categories" creates them).
	Categories []UsenetCategory `json:"categories"`
	Missing    []string         `json:"missing_categories"`
	// CompleteFolder is where finished jobs land, and whether Arrmada sees it (after the
	// client's path mappings).
	CompleteFolder PathCheck `json:"complete_folder"`
	// Servers are SABnzbd's enabled news servers. ServersWorking counts those without an
	// error; none at all means nothing can download.
	Servers           []UsenetServer `json:"servers"`
	ServersConfigured int            `json:"servers_configured"`
	ServersWorking    int            `json:"servers_working"`
	// Language is SABnzbd's interface language. Its failure reasons are translated, so a
	// language other than English makes them harder to classify (NotEnglish).
	Language   string `json:"language"`
	NotEnglish bool   `json:"not_english"`
	// Warnings are the problems above as plain sentences, worst first.
	Warnings []string `json:"warnings"`
}

// UsenetCategory is one of Arrmada's categories in SABnzbd.
type UsenetCategory struct {
	Name    string `json:"name"`
	For     string `json:"for"` // movies, tv, books, music
	Present bool   `json:"present"`
	// Folder is set when the category has a folder of its own, with whether Arrmada sees it.
	Folder *PathCheck `json:"folder,omitempty"`
}

// UsenetServer is one news server SABnzbd uses. No host, login or password: the card only
// needs to know whether it works.
type UsenetServer struct {
	Name        string `json:"name"`
	Optional    bool   `json:"optional"`
	Connections int    `json:"connections"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
}

// Facts reads what the card shows. Only reads: nothing in SABnzbd is changed.
func (s *SABnzbd) Facts(ctx context.Context, dc Client) (UsenetFacts, error) {
	var f UsenetFacts
	st, err := s.fullStatus(ctx, dc)
	if err != nil {
		return f, err
	}
	f.Version = st.Status.Version
	cats, err := s.Categories(ctx, dc)
	if err != nil {
		return f, err
	}
	folders, err := s.categoryFolders(ctx, dc, st.Status.CompleteDir)
	if err != nil {
		return f, err
	}
	f.Categories, f.Missing = []UsenetCategory{}, []string{}
	for _, c := range s.categories() {
		uc := UsenetCategory{Name: c.name, For: c.kind, Present: hasCategory(cats, c.name)}
		if !uc.Present {
			f.Missing = append(f.Missing, c.name)
		} else if dir, ok := folders[strings.ToLower(c.name)]; ok {
			pc := folderCheck(dc, "category_folder", dir)
			pc.Category = c.name
			uc.Folder = &pc
		}
		f.Categories = append(f.Categories, uc)
	}
	f.CompleteFolder = folderCheck(dc, "complete_folder", st.Status.CompleteDir)

	f.Servers = []UsenetServer{}
	for _, sv := range st.Status.Servers {
		us := UsenetServer{Name: sv.Name, Optional: sv.Optional != 0, Connections: int(sv.Total), Error: scrub(dc, sv.Error)}
		us.OK = us.Error == ""
		if us.OK {
			f.ServersWorking++
		}
		f.Servers = append(f.Servers, us)
	}
	f.ServersConfigured = len(f.Servers)

	if body, err := s.call(ctx, dc, "get_config", url.Values{"section": {"misc"}, "keyword": {"language"}}); err == nil {
		if v, err := decode[struct {
			Config struct {
				Misc struct {
					Language string `json:"language"`
				} `json:"misc"`
			} `json:"config"`
		}](dc, body, "language"); err == nil {
			f.Language = v.Config.Misc.Language
		}
	}
	f.NotEnglish = f.Language != "" && !strings.HasPrefix(strings.ToLower(f.Language), "en")
	f.Warnings = factWarnings(dc, f)
	return f, nil
}

func folderCheck(dc Client, what, remote string) PathCheck {
	local := dc.PathMaps.ToLocal(remote)
	return PathCheck{What: what, Remote: remote, Local: local, Visible: isDir(local)}
}

func factWarnings(dc Client, f UsenetFacts) []string {
	w := []string{}
	if f.ServersConfigured == 0 {
		w = append(w, fmt.Sprintf("%s has no news server, so nothing can download. Add your provider in SABnzbd → Config → Servers", dc.Name))
	} else if f.ServersWorking == 0 {
		w = append(w, fmt.Sprintf("None of %s's news servers is working. Check the provider's address, login and connections in SABnzbd → Config → Servers", dc.Name))
	}
	if len(f.Missing) > 0 {
		w = append(w, fmt.Sprintf("%s is missing Arrmada's %s %s. Use \"Add missing categories\"", dc.Name, strings.Join(f.Missing, ", "), plural(len(f.Missing), "category", "categories")))
	}
	if f.CompleteFolder.Remote != "" && !f.CompleteFolder.Visible {
		w = append(w, fmt.Sprintf("Arrmada can't see %s's completed folder %s (looked for it at %s). Mount it into Arrmada or add a path mapping", dc.Name, f.CompleteFolder.Remote, f.CompleteFolder.Local))
	}
	for _, c := range f.Categories {
		if c.Folder != nil && !c.Folder.Visible {
			w = append(w, fmt.Sprintf("Arrmada can't see the folder %s files %s downloads in, %s", dc.Name, c.Name, c.Folder.Remote))
		}
	}
	if f.NotEnglish {
		w = append(w, fmt.Sprintf("%s's language is %q. Its failure reasons are translated, which makes them harder for Arrmada to read; English works best (SABnzbd → Config → General)", dc.Name, f.Language))
	}
	return w
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// AddMissingCategories creates the Arrmada categories SABnzbd lacks, with SABnzbd's defaults
// (no folder of their own), and names the ones it created. It's only ever run when the owner
// asks: Arrmada doesn't change a SABnzbd it was pointed at on its own.
func (s *SABnzbd) AddMissingCategories(ctx context.Context, dc Client) ([]string, error) {
	cats, err := s.Categories(ctx, dc)
	if err != nil {
		return nil, err
	}
	created := []string{}
	for _, c := range s.categories() {
		if hasCategory(cats, c.name) {
			continue
		}
		if _, err := s.call(ctx, dc, "set_config", url.Values{"section": {"categories"}, "keyword": {c.name}, "name": {c.name}}); err != nil {
			return created, fmt.Errorf("couldn't add the %q category: %w", c.name, err)
		}
		created = append(created, c.name)
	}
	// Read back: a category SABnzbd didn't keep must not read as added.
	after, err := s.Categories(ctx, dc)
	if err != nil {
		return created, err
	}
	for _, c := range created {
		if !hasCategory(after, c) {
			return created, fmt.Errorf("SABnzbd didn't keep the %q category", c)
		}
	}
	return created, nil
}
