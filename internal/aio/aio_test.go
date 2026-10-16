package aio

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestReadOptionsDefaultsToBothOn(t *testing.T) {
	o, notes := ReadOptions(envOf(map[string]string{"PUID": "99", "PGID": "100", "ARRMADA_DOWNLOADS_DIR": "/storage/downloads"}), true)
	if !o.Qbittorrent || !o.FlareSolverr {
		t.Fatalf("both companions should default on: %+v", o)
	}
	if o.UID != 99 || o.GID != 100 || o.DataDir != "/data" || o.Downloads != "/storage/downloads" || o.TorrentPort != DefaultTorrentPort {
		t.Errorf("options = %+v", o)
	}
	if o.QbittorrentProfile() != "/data/qbittorrent" {
		t.Errorf("profile = %q", o.QbittorrentProfile())
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "qBittorrent on, bundled FlareSolverr on") {
		t.Errorf("notes = %q", notes)
	}
}

func TestReadOptionsSwitches(t *testing.T) {
	for _, tc := range []struct {
		v      string
		on, ok bool
	}{{"", true, true}, {"true", true, true}, {"1", true, true}, {"Yes", true, true}, {"false", false, true}, {" FALSE ", false, true}, {"0", false, true}, {"off", false, true}, {"no", false, true}, {"flase", true, false}} {
		on, ok := parseSwitch(tc.v)
		if on != tc.on || ok != tc.ok {
			t.Errorf("parseSwitch(%q) = %v, %v; want %v, %v", tc.v, on, ok, tc.on, tc.ok)
		}
	}
	o, notes := ReadOptions(envOf(map[string]string{"ARRMADA_BUNDLE_QBITTORRENT": "false", "ARRMADA_BUNDLE_FLARESOLVERR": "flase", "ARRMADA_QBIT_PORT": "70000"}), true)
	if o.Qbittorrent || !o.FlareSolverr || o.TorrentPort != DefaultTorrentPort {
		t.Errorf("options = %+v", o)
	}
	if len(notes) != 3 {
		t.Errorf("want notes for the typo and the bad port, got %q", notes)
	}
}

// The ARM build has no FlareSolverr: asking for it says so and leaves it off.
func TestReadOptionsNoFlareSolverrInImage(t *testing.T) {
	o, notes := ReadOptions(envOf(nil), false)
	if o.FlareSolverr {
		t.Fatal("FlareSolverr on in an image without it")
	}
	if !strings.Contains(strings.Join(notes, "\n"), "ARM") {
		t.Errorf("notes = %q", notes)
	}
}

func TestAppEnv(t *testing.T) {
	on := Options{Qbittorrent: true, FlareSolverr: true, TorrentPort: 50000, DataDir: "/data"}
	got := strings.Join(AppEnv(on, envOf(nil)), "\n")
	for _, want := range []string{
		"ARRMADA_AIO_QBITTORRENT_URL=http://127.0.0.1:8080",
		"ARRMADA_QBITTORRENT_URL=http://127.0.0.1:8080",
		"ARRMADA_QBIT_PORT=50000",
		"ARRMADA_QBITTORRENT_LOGIN=/data/qbittorrent/arrmada-webui-login.json",
		"ARRMADA_FLARESOLVERR_URL=http://127.0.0.1:8191",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	// The owner's own FlareSolverr address wins.
	if got := strings.Join(AppEnv(on, envOf(map[string]string{"ARRMADA_FLARESOLVERR_URL": "http://10.0.0.2:8191"})), "\n"); strings.Contains(got, "FLARESOLVERR") {
		t.Errorf("overrode the owner's FlareSolverr:\n%s", got)
	}
	// Switched off: no client is registered, even from a value left in the template, but
	// Arrmada still learns where the bundled one was.
	env := MergeEnv([]string{"ARRMADA_QBITTORRENT_URL=http://x:8080", "PUID=99"}, AppEnv(Options{}, envOf(nil)))
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	if m["ARRMADA_QBITTORRENT_URL"] != "" || m["ARRMADA_AIO_QBITTORRENT_URL"] != LocalURL || m["PUID"] != "99" || m["ARRMADA_FLARESOLVERR_URL"] != "" {
		t.Errorf("off env = %v", m)
	}
}

func TestPlan(t *testing.T) {
	o := Options{UID: 99, GID: 100, DataDir: "/data", Qbittorrent: true, FlareSolverr: true, TorrentPort: 56881}
	environ := []string{"ARRMADA_TMDB_API_KEY=secret", "TZ=Europe/Oslo", "PUID=99"}
	svcs := Plan(o, environ, envOf(map[string]string{"TZ": "Europe/Oslo"}))
	if len(svcs) != 3 || svcs[0].Name != "arrmada" || !svcs[0].Main || svcs[0].UID != nil {
		t.Fatalf("services = %+v", svcs)
	}
	if !strings.Contains(strings.Join(svcs[0].Env, "\n"), "ARRMADA_TMDB_API_KEY=secret") {
		t.Error("Arrmada lost its own environment")
	}
	for _, s := range svcs[1:] {
		if s.UID == nil || s.GID == nil || s.Main {
			t.Fatalf("%s has no user to run as", s.Name)
		}
		// qBittorrent writes the owner's downloads, so it's PUID:PGID; FlareSolverr's
		// sandbox-less Chromium gets a user of its own that owns none of the owner's files.
		want := [2]int{99, 100}
		if s.Name == "flaresolverr" {
			want = [2]int{FlareSolverrUID, FlareSolverrGID}
		}
		if got := [2]int{*s.UID, *s.GID}; got != want {
			t.Errorf("%s runs as %v, want %v", s.Name, got, want)
		}
		env := strings.Join(s.Env, "\n")
		if strings.Contains(env, "secret") {
			t.Errorf("%s was given Arrmada's keys", s.Name)
		}
		if !strings.Contains(env, "TZ=Europe/Oslo") || !strings.Contains(env, "LANG=C.UTF-8") {
			t.Errorf("%s env = %q", s.Name, env)
		}
	}
	if q := svcs[1]; q.Name != "qbittorrent" || strings.Join(q.Args, " ") != "--profile=/data/qbittorrent --webui-port=8080 --confirm-legal-notice" {
		t.Errorf("qbittorrent = %+v", q)
	}
	if f := svcs[2]; f.Name != "flaresolverr" || !strings.Contains(strings.Join(f.Env, " "), "HOST=127.0.0.1") ||
		f.Dir != FlareSolverrHome || !strings.Contains(strings.Join(f.Env, " "), "HOME="+FlareSolverrHome) {
		t.Errorf("flaresolverr = %+v", f)
	}
	if !svcs[2].OwnUser || svcs[1].OwnUser {
		t.Error("only FlareSolverr has a user of its own (qBittorrent shares PUID with Arrmada)")
	}
	if FlareSolverrUID == 99 || FlareSolverrUID == 1000 || FlareSolverrUID == 0 {
		t.Errorf("FlareSolverr's user %d is one a PUID commonly is", FlareSolverrUID)
	}
	if got := Plan(Options{}, nil, envOf(nil)); len(got) != 1 {
		t.Errorf("both off still runs %d services", len(got))
	}
}

func TestSeedQbittorrent(t *testing.T) {
	profile := t.TempDir()
	seeded, err := SeedQbittorrent(SeedOptions{Profile: profile, Downloads: "/storage/downloads/", TorrentPort: 56881, UID: os.Getuid(), GID: os.Getgid()})
	if err != nil || !seeded {
		t.Fatalf("seed: %v, %v", seeded, err)
	}
	confPath := filepath.Join(profile, "qBittorrent", "config", "qBittorrent.conf")
	raw, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	conf := string(raw)
	for _, want := range []string{
		`Session\DefaultSavePath=/storage/downloads` + "\n",
		`Session\TempPath=/storage/downloads/incomplete`,
		`Session\Port=56881`,
		`WebUI\Port=8080`,
		`WebUI\Username=admin`,
		`WebUI\LocalHostAuth=true`,
		`WebUI\HostHeaderValidation=false`,
		`WebUI\AuthSubnetWhitelistEnabled=false`,
		`WebUI\CSRFProtection=true`,
		`[LegalNotice]` + "\n" + `Accepted=true`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	login, err := ReadLogin(LoginPath(profile))
	if err != nil {
		t.Fatal(err)
	}
	if login.Username != "admin" || len(login.Password) != 24 {
		t.Errorf("login = %+v", login)
	}
	if strings.Contains(conf, login.Password) {
		t.Error("the config holds the password itself, not its hash")
	}
	// The stored hash is qBittorrent's PBKDF2 form of exactly that password.
	m := regexp.MustCompile(`WebUI\\Password_PBKDF2="@ByteArray\(([^:]+):([^)]+)\)"`).FindStringSubmatch(conf)
	if m == nil {
		t.Fatalf("no PBKDF2 password in:\n%s", conf)
	}
	salt, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil || len(salt) != 16 {
		t.Fatalf("salt %q: %v", m[1], err)
	}
	again, err := hashWithSalt(login.Password, salt)
	if err != nil || again != m[1]+":"+m[2] {
		t.Errorf("hash doesn't match the password: %v", err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(LoginPath(profile)); fi.Mode().Perm() != 0o600 {
			t.Errorf("login file mode %v", fi.Mode().Perm())
		}
	}

	// A second start leaves qBittorrent's config and the login alone.
	if err := os.WriteFile(confPath, []byte("changed by qBittorrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	seeded, err = SeedQbittorrent(SeedOptions{Profile: profile, Downloads: "/elsewhere"})
	if err != nil || seeded {
		t.Fatalf("second seed: %v, %v", seeded, err)
	}
	if raw, _ := os.ReadFile(confPath); string(raw) != "changed by qBittorrent" {
		t.Error("an existing config was rewritten")
	}
	if l2, _ := ReadLogin(LoginPath(profile)); l2 != login {
		t.Error("the login changed on a second start")
	}
}

// /data loses its "others" bits (and only those), so FlareSolverr's user can't get in.
func TestCloseToOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o775|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if err := CloseToOthers(dir, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o770 || fi.Mode()&os.ModeSetgid == 0 {
		t.Errorf("mode %v, want 0770 with setgid kept", fi.Mode())
	}
	// A missing folder is made closed.
	fresh := filepath.Join(t.TempDir(), "new")
	if err := CloseToOthers(fresh, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm()&0o007 != 0 {
		t.Errorf("new folder mode %v", fi.Mode())
	}
	// As root (the supervisor), the folder itself is handed to PUID:PGID before it's
	// closed, so the companions running as them can still get in.
	if os.Geteuid() == 0 {
		owned := filepath.Join(t.TempDir(), "owned")
		if err := CloseToOthers(owned, 99, 100); err != nil {
			t.Fatal(err)
		}
		if st, ok := statOwner(owned); !ok || st != [2]int{99, 100} {
			t.Errorf("owner %v, want 99:100", st)
		}
	}
}

func TestPasswordsDiffer(t *testing.T) {
	a, _ := randomPassword()
	b, _ := randomPassword()
	if a == b || len(a) != 24 {
		t.Errorf("passwords %q, %q", a, b)
	}
}

func TestReadLoginRejectsIncomplete(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.json")
	_ = os.WriteFile(p, []byte(`{"username":"admin"}`), 0o600)
	if _, err := ReadLogin(p); err == nil {
		t.Error("a login without a password was accepted")
	}
	if _, err := ReadLogin(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file was accepted")
	}
}
