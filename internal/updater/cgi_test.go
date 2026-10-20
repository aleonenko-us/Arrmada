package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runCGI runs docker/updater/updater.cgi the way busybox httpd does (request details in
// the environment, the answer on stdout) and returns the status line and the JSON body.
func runCGI(t *testing.T, dir, token, method, query, auth string) (status, body string) {
	t.Helper()
	script, err := filepath.Abs("../../docker/updater/updater.cgi")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(),
		"ARRMADA_HOST_DIR="+dir, "ARRMADA_UPDATER_TOKEN="+token,
		"REQUEST_METHOD="+method, "QUERY_STRING="+query, "HTTP_AUTHORIZATION="+auth)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("updater.cgi: %v", err)
	}
	head, rest, _ := strings.Cut(string(out), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if s, ok := strings.CutPrefix(line, "Status: "); ok {
			status = s
		}
	}
	return status, strings.TrimSpace(rest)
}

func TestUpdaterCGI(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	// A stand-in for update.sh that takes a moment, so "already running" can be seen.
	script := "#!/bin/sh\necho 'backing up'\nsleep 2\necho 'installed v9'\n"
	if err := os.WriteFile(filepath.Join(dir, "update.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const tok = "s3cret"
	good := "Bearer " + tok

	if st, _ := runCGI(t, dir, tok, "GET", "status", ""); !strings.HasPrefix(st, "401") {
		t.Errorf("no credentials: %q, want 401", st)
	}
	if st, _ := runCGI(t, dir, tok, "GET", "status", "Bearer wrong"); !strings.HasPrefix(st, "401") {
		t.Errorf("wrong token: %q, want 401", st)
	}
	// With no token configured the updater refuses everything, even a matching empty one.
	if st, _ := runCGI(t, dir, "", "GET", "status", "Bearer "); !strings.HasPrefix(st, "503") {
		t.Errorf("no token set: %q, want 503", st)
	}
	if st, body := runCGI(t, dir, tok, "GET", "status", good); !strings.HasPrefix(st, "200") || !strings.Contains(body, `"state":"idle"`) {
		t.Errorf("idle status: %q %s", st, body)
	}
	if st, _ := runCGI(t, dir, tok, "GET", "nonsense", good); !strings.HasPrefix(st, "404") {
		t.Errorf("unknown request: %q, want 404", st)
	}

	if st, body := runCGI(t, dir, tok, "POST", "apply", good); !strings.HasPrefix(st, "202") || !strings.Contains(body, `"running"`) {
		t.Fatalf("apply: %q %s", st, body)
	}
	if st, _ := runCGI(t, dir, tok, "POST", "apply", good); !strings.HasPrefix(st, "409") {
		t.Errorf("a second apply while it runs: %q, want 409", st)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		st, body := runCGI(t, dir, tok, "GET", "status", good)
		if strings.Contains(body, `"state":"done"`) {
			if !strings.HasPrefix(st, "200") || !strings.Contains(body, "installed v9") || !strings.Contains(body, `\n`) {
				t.Errorf("finished status: %q %s", st, body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the update never finished: %s", body)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// A failing update.sh reads as failed, with its log.
	if err := os.WriteFile(filepath.Join(dir, "update.sh"), []byte("#!/bin/sh\necho 'no space left'\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if st, _ := runCGI(t, dir, tok, "POST", "apply", good); !strings.HasPrefix(st, "202") {
		t.Fatalf("second apply: %q", st)
	}
	for {
		_, body := runCGI(t, dir, tok, "GET", "status", good)
		if strings.Contains(body, `"state":"failed"`) {
			if !strings.Contains(body, "no space left") {
				t.Errorf("failed status lacks the log: %s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the failing update never reported failed: %s", body)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
