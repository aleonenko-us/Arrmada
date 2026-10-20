package automation

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The library scan at scale (MUS-37): ARRMADA_SCAN_BENCH=<tracks> (100000 when set to 1)
// generates a library of small synthetic FLAC files (real FLAC headers and Vorbis
// comments, sparse to 300 KB) laid out Artist/Album/NN - Title.flac, 10 tracks an album and
// 10 albums an artist, with the artists and albums in the library through a stand-in for
// MusicBrainz. It times the walk and the tag reads on their own, then a first scan (every
// file read, matched and recorded) and a second, incremental one (nothing opened), and
// logs the numbers. Off by default: it writes 100,000 files.
func TestMusicScanBench(t *testing.T) {
	n := os.Getenv("ARRMADA_SCAN_BENCH")
	if n == "" {
		t.Skip("set ARRMADA_SCAN_BENCH=1 (100,000 tracks) or a track count to run")
	}
	tracks, _ := strconv.Atoi(n)
	if tracks <= 1 {
		tracks = 100000
	}
	const perAlbum, perArtist = 10, 10
	albums := tracks / perAlbum
	artists := (albums + perArtist - 1) / perArtist

	h := musicTestCoord(t)
	root := h.c.imp.MusicDir()
	listing := make([]metadata.TrackResult, perAlbum)
	for i := range listing {
		listing[i] = metadata.TrackResult{Title: fmt.Sprintf("Song %02d", i+1), Disc: 1, Number: i + 1, DurationSec: 200}
	}

	start := time.Now()
	h.fake.mu.Lock()
	for a := 0; a < artists; a++ {
		mbid := fmt.Sprintf("bench-artist-%04d", a)
		h.fake.artists[mbid] = metadata.ArtistDetails{ArtistResult: metadata.ArtistResult{MBID: mbid, Name: benchArtist(a)}}
		var rgs []metadata.AlbumResult
		for b := 0; b < perArtist && a*perArtist+b < albums; b++ {
			rg := fmt.Sprintf("%s-rg-%02d", mbid, b)
			rgs = append(rgs, metadata.AlbumResult{MBID: rg, Title: benchAlbum(b), Type: "Album"})
			h.fake.tracks[rg] = listing
		}
		h.fake.albums[mbid] = rgs
	}
	h.fake.mu.Unlock()
	for a := 0; a < artists; a++ {
		if _, err := h.svc.AddArtist(h.ctx, fmt.Sprintf("bench-artist-%04d", a), "", false); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("library: %d artists, %d albums in the database in %v", artists, albums, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var made atomic.Int64
	for al := 0; al < albums; al++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(al int) {
			defer wg.Done()
			defer func() { <-sem }()
			a, b := al/perArtist, al%perArtist
			dir := filepath.Join(root, benchArtist(a), benchAlbum(b))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Error(err)
				return
			}
			for i := 0; i < perAlbum; i++ {
				p := filepath.Join(dir, fmt.Sprintf("%02d - Song %02d.flac", i+1, i+1))
				if err := writeBenchFLAC(p, benchArtist(a), benchAlbum(b), fmt.Sprintf("Song %02d", i+1), i+1); err != nil {
					t.Error(err)
					return
				}
				made.Add(1)
			}
		}(al)
	}
	wg.Wait()
	t.Logf("files: %d synthetic FLACs written in %v", made.Load(), time.Since(start).Round(time.Millisecond))

	// The two I/O phases on their own: the walk, and reading every file's tags and headers.
	workers := 4
	start = time.Now()
	dirs, err := walkMusicRoot(h.ctx, h.c.log, root, workers)
	if err != nil {
		t.Fatal(err)
	}
	walk := time.Since(start)
	var paths []string
	for _, d := range dirs {
		paths = append(paths, d.audio...)
	}
	start = time.Now()
	jobs := make(chan string)
	var bad atomic.Int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if tags, props, err := readAudioFacts(p); err != nil || tags.Title == "" || props == nil || props.DurationSec < 199 {
					bad.Add(1)
				}
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	read := time.Since(start)
	if bad.Load() > 0 {
		t.Fatalf("%d files' tags didn't read back", bad.Load())
	}
	t.Logf("walk: %d folders, %d files in %v; tags and headers of every file read with %d workers in %v (%.0f files/s)",
		len(dirs), len(paths), walk.Round(time.Millisecond), workers, read.Round(time.Millisecond), float64(len(paths))/read.Seconds())

	start = time.Now()
	first := h.scan(t, MusicScanOptions{Workers: workers, FoldersPerRun: albums + 1})
	t.Logf("first scan: %v — %+v", time.Since(start).Round(time.Millisecond), first)
	if first.Tracks != tracks || first.Reviews != 0 {
		t.Errorf("first scan recorded %d of %d tracks (%d reviews)", first.Tracks, tracks, first.Reviews)
	}
	start = time.Now()
	second := h.scan(t, MusicScanOptions{Workers: workers, FoldersPerRun: albums + 1})
	t.Logf("second scan (nothing changed): %v — %+v", time.Since(start).Round(time.Millisecond), second)
	if second.Opened != 0 || second.Skipped != albums {
		t.Errorf("second scan opened %d files and skipped %d of %d folders", second.Opened, second.Skipped, albums)
	}
}

func benchArtist(a int) string { return fmt.Sprintf("Bench Artist %04d", a) }
func benchAlbum(b int) string  { return fmt.Sprintf("Bench Album %02d", b) }

// writeBenchFLAC writes a small FLAC: the marker, a STREAMINFO saying 200 s of 44.1 kHz
// 16-bit stereo, Vorbis comments, a few stand-in frame bytes, then a sparse tail to 300 KB
// so the scan's track-size floor takes it.
func writeBenchFLAC(path, artist, album, title string, track int) error {
	si := make([]byte, 34)
	const samples = 44100 * 200
	// Bytes 10..17: 20 bits sample rate, 3 bits channels-1, 5 bits bps-1, 36 bits samples.
	v := uint64(44100)<<44 | uint64(1)<<41 | uint64(15)<<36 | uint64(samples)
	binary.BigEndian.PutUint64(si[10:18], v)
	var vc bytes.Buffer
	vendor := "arrmada-bench"
	_ = binary.Write(&vc, binary.LittleEndian, uint32(len(vendor)))
	vc.WriteString(vendor)
	comments := []string{"TITLE=" + title, "ARTIST=" + artist, "ALBUMARTIST=" + artist, "ALBUM=" + album, "TRACKNUMBER=" + strconv.Itoa(track)}
	_ = binary.Write(&vc, binary.LittleEndian, uint32(len(comments)))
	for _, c := range comments {
		_ = binary.Write(&vc, binary.LittleEndian, uint32(len(c)))
		vc.WriteString(c)
	}
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x00, 0, 0, 34}) // STREAMINFO, not last
	b.Write(si)
	n := vc.Len()
	b.Write([]byte{0x84, byte(n >> 16), byte(n >> 8), byte(n)}) // VORBIS_COMMENT, last
	b.Write(vc.Bytes())
	b.Write(bytes.Repeat([]byte{0xFF, 0xF8, 0x12, 0x34}, 64))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(b.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := f.Truncate(300 << 10); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
