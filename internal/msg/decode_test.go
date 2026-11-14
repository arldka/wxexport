package msg

import (
	"testing"
)

func TestSplitLocalType(t *testing.T) {
	cases := []struct {
		in       int64
		wantType int64
		wantSub  int64
	}{
		{1, 1, 0},
		{3, 3, 0},
		{50, 50, 0},
		{244813135921, 49, 57}, // real value observed: appmsg quote
		{21474836529, 49, 5},   // real value observed: appmsg link
		{25769803825, 49, 6},   // real value observed: appmsg file
	}
	for _, c := range cases {
		gt, gs := SplitLocalType(c.in)
		if gt != c.wantType || gs != c.wantSub {
			t.Errorf("SplitLocalType(%d) = (%d,%d), want (%d,%d)", c.in, gt, gs, c.wantType, c.wantSub)
		}
	}
}

func TestIsZstdAndDecodeContent(t *testing.T) {
	plain := "hello"
	got, err := DecodeContent([]byte(plain), 0, true)
	if err != nil || got != plain {
		t.Fatalf("plain: %v %q", err, got)
	}
	if IsZstd([]byte(plain)) {
		t.Error("plain text misdetected as zstd")
	}
	compressed := zstdCompress(t, "zstd round trip")
	if !IsZstd(compressed) {
		t.Error("compressed bytes not detected as zstd")
	}
	got, err = DecodeContent(compressed, 4, true)
	if err != nil || got != "zstd round trip" {
		t.Fatalf("zstd: %v %q", err, got)
	}
	// Flag absent/0 but bytes are still zstd magic: trust the magic (source of truth per spec).
	got, err = DecodeContent(compressed, 0, true)
	if err != nil || got != "zstd round trip" {
		t.Fatalf("zstd without flag: %v %q", err, got)
	}
	if got, err := DecodeContent(nil, 0, false); err != nil || got != "" {
		t.Fatalf("empty: %v %q", err, got)
	}
}

func TestDecodeText(t *testing.T) {
	d := Decode(1, "hello world", "")
	if d.Kind != KindText || d.Text != "hello world" || d.Type != 1 || d.Sub != 0 {
		t.Fatalf("%+v", d)
	}
}

// --- image/sticker/voice/video (real-shaped, anonymized) ---

func TestDecodeImage(t *testing.T) {
	content := `<?xml version="1.0"?><msg><img aeskey="aa" encryver="1" length="12345" md5="deadbeefdeadbeefdeadbeefdeadbeef" /></msg>`
	d := Decode(3, content, "")
	if d.Kind != KindImage || d.Media == nil || d.Media.MD5 != "deadbeefdeadbeefdeadbeefdeadbeef" || d.Media.SizeBytes != 12345 {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeSticker(t *testing.T) {
	// Calibrated: stickers use `len`, not `length`.
	content := `<msg><emoji fromusername="wxid_a" tousername="wxid_b" type="2" md5="cafebabecafebabecafebabecafebabe" len="2048" ...></emoji></msg>`
	d := Decode(47, content, "")
	if d.Kind != KindSticker || d.Media == nil || d.Media.SizeBytes != 2048 || d.Media.MD5 != "cafebabecafebabecafebabecafebabe" {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeVoice(t *testing.T) {
	content := `<msg><voicemsg voicelength="4250" ...></voicemsg></msg>`
	d := Decode(34, content, "")
	if d.Kind != KindVoice || d.Media == nil || d.Media.DurationMS != 4250 {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeVideo(t *testing.T) {
	content := `<msg><videomsg playlength="12" md5="0123456789abcdef0123456789abcdef" ...></videomsg></msg>`
	d := Decode(43, content, "")
	if d.Kind != KindVideo || d.Media == nil || d.Media.DurationS != 12 || d.Media.MD5 != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("%+v", d)
	}
}

// --- location ---

func TestDecodeLocation(t *testing.T) {
	content := `<?xml version="1.0"?><msg><location x="12.345678" y="98.765432" scale="15" label="" poiname="Coffee Place" maptype="0" /></msg>`
	d := Decode(48, content, "")
	if d.Kind != KindLocation || d.Location == nil {
		t.Fatalf("%+v", d)
	}
	if d.Location.Label != "Coffee Place" || d.Location.Lat != 12.345678 || d.Location.Lng != 98.765432 {
		t.Fatalf("%+v", d.Location)
	}
}

func TestDecodeLocationPrefersLabelOverPoiname(t *testing.T) {
	content := `<msg><location x="1" y="2" label="Home" poiname="Should not use this" /></msg>`
	d := Decode(48, content, "")
	if d.Location.Label != "Home" {
		t.Errorf("label = %q", d.Location.Label)
	}
}

// --- system / recall ---

func TestDecodeRecall(t *testing.T) {
	content := `<?xml version="1.0"?><sysmsg type="revokemsg"><revokemsg><content>"Alice" recalled a message</content><revoketime>0</revoketime></revokemsg></sysmsg>`
	d := Decode(10000, content, "")
	if d.Kind != KindRecall {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeSystemOther(t *testing.T) {
	content := `<sysmsg type="pat"><pat><content>Alice patted Bob</content></pat></sysmsg>`
	d := Decode(10000, content, "")
	if d.Kind != KindSystem || d.Text != "Alice patted Bob" {
		t.Fatalf("%+v", d)
	}
}

// --- appmsg: link/file/quote/forward/applet ---

func TestDecodeLink(t *testing.T) {
	content := `<?xml version="1.0"?><msg><appmsg appid="wxabc"><title>A nice place</title><des>Some city</des><type>5</type><url>https://example.com/x</url></appmsg></msg>`
	d := Decode(21474836529, content, "") // (49,5)
	if d.Kind != KindLink || d.Link == nil || d.Link.Title != "A nice place" || d.Link.URL != "https://example.com/x" {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeFile(t *testing.T) {
	content := `<?xml version="1.0"?><msg><appmsg appid="wxabc"><title>report.pdf</title><type>6</type><appattach><totallen>12345</totallen><fileext>pdf</fileext></appattach></appmsg></msg>`
	d := Decode(25769803825, content, "") // (49,6)
	if d.Kind != KindFile || d.File == nil || d.File.Title != "report.pdf" || d.File.Ext != "pdf" || d.File.SizeBytes != 12345 {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeQuote(t *testing.T) {
	content := `<?xml version="1.0"?><msg><appmsg><title>Sure, sounds good</title><type>57</type><refermsg><type>1</type><fromusr>wxid_a</fromusr><chatusr>wxid_a</chatusr><displayname>Alice</displayname><content>What time?</content><createtime>1700000000</createtime></refermsg></appmsg></msg>`
	d := Decode(244813135921, content, "") // (49,57)
	if d.Kind != KindQuote || d.Text != "Sure, sounds good" || d.Quote == nil {
		t.Fatalf("%+v", d)
	}
	if d.Quote.DisplayName != "Alice" || d.Quote.Text != "What time?" || d.Quote.CreateTime != 1700000000 {
		t.Fatalf("%+v", d.Quote)
	}
}

func TestDecodeForwardAndApplet(t *testing.T) {
	fwd := Decode(81604378673, `<msg><appmsg><title>Forwarded chat</title><type>19</type></appmsg></msg>`, "") // (49,19)
	if fwd.Kind != KindForward || fwd.AppTitle != "Forwarded chat" {
		t.Fatalf("%+v", fwd)
	}
	applet := Decode(17179869233, `<msg><appmsg><title>Mini Program</title><type>4</type></appmsg></msg>`, "") // (49,4)
	if applet.Kind != KindApplet || applet.AppTitle != "Mini Program" {
		t.Fatalf("%+v", applet)
	}
}

func TestDecodeUnknownAppSubtypeFallsBackToApp(t *testing.T) {
	d := Decode(poke(49, 999), `<msg><appmsg><title>Something new</title><type>999</type></appmsg></msg>`, "")
	if d.Kind != KindApp || d.AppTitle != "Something new" {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeUnknownType(t *testing.T) {
	d := Decode(poke(7777, 0), "whatever", "")
	if d.Kind != KindUnknown || d.Type != 7777 {
		t.Fatalf("%+v", d)
	}
}

func poke(t, sub int64) int64 { return int64(uint64(sub)<<32 | uint64(t)) }
