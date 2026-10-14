package torrentmeta

import (
	"strings"
	"testing"
)

const sampleNZB = `<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
<head><meta type="name">Film.2020.1080p.WEB-DL-GRP</meta></head>
<file poster="p" date="1" subject="Film [1/2] - &quot;film.2020.part01.rar&quot; yEnc (1/2)">
<groups><group>a.b.c</group></groups>
<segments><segment bytes="700" number="1">a@b</segment><segment bytes="300" number="2">c@d</segment></segments>
</file>
<file poster="p" date="1" subject="Film [2/2] - &quot;film.2020.par2&quot; yEnc (1/1)">
<groups><group>a.b.c</group></groups>
<segments><segment bytes="50" number="1">e@f</segment></segments>
</file>
</nzb>`

func TestParseNZB(t *testing.T) {
	if !LooksLikeNZB([]byte(sampleNZB)) || LooksLikeNZB([]byte("d4:infod4:name4:testee")) || LooksLikeNZB([]byte("<html><body>login</body></html>")) {
		t.Fatal("LooksLikeNZB misreads")
	}
	info, err := ParseNZB([]byte(sampleNZB), "upload.nzb")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Film.2020.1080p.WEB-DL-GRP" || info.SizeBytes != 1050 || len(info.Files) != 2 ||
		info.Files[0].Path != "film.2020.part01.rar" || info.Files[0].SizeBytes != 1000 || info.Files[1].Path != "film.2020.par2" {
		t.Errorf("info = %+v", info)
	}
	// No name in the head: the file's name stands in.
	noName := strings.Replace(sampleNZB, `<meta type="name">Film.2020.1080p.WEB-DL-GRP</meta>`, "", 1)
	if info, err := ParseNZB([]byte(noName), `C:\dl\Other.Film.nzb`); err != nil || info.Name != "Other.Film" {
		t.Errorf("unnamed = %+v, %v", info, err)
	}
	if _, err := ParseNZB([]byte(`<nzb></nzb>`), "x.nzb"); err == nil {
		t.Error("an NZB with no files was accepted")
	}
}
