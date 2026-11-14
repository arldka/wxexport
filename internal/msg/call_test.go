package msg

import (
	"bytes"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func zstdCompress(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// voipXMLFixture builds the real <voipmsg> shape (docs/FORMAT.md) around one <msg> text, with
// the <duration> field always 0 — exactly as observed in real data (it must never be trusted).
func voipXMLFixture(msgText string, msgType, roomType int) string {
	return `<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[` + msgText + `]]></msg>
<room_type>` + itoa(roomType) + `</room_type>
<red_dot>false</red_dot>
<roomid>1234567890123456789</roomid>
<roomkey>0</roomkey>
<inviteid>123456789</inviteid>
<msg_type>` + itoa(msgType) + `</msg_type>
<timestamp>1700000000000</timestamp>
<identity><![CDATA[9876543210987654321]]></identity>
<duration>0</duration>
<inviteid64>1700000000000</inviteid64>
<business>1</business>
<caller_memberid>0</caller_memberid>
<callee_memberid>1</callee_memberid>
<force_update>0</force_update>
</VoIPBubbleMsg></voipmsg>`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// TestDecodeCallRealCorpus replays the distinct <msg> text forms actually observed across 97
// real calls in one chat (docs/FORMAT.md), confirming every one is classified and never dropped.
func TestDecodeCallRealCorpus(t *testing.T) {
	cases := []struct {
		msgText     string
		msgType     int
		wantOutcome CallOutcome
		wantSeconds int64
	}{
		{"Duration: 39:18", 100, CallCompleted, 39*60 + 18},
		{"Duration: 18:57", 100, CallCompleted, 18*60 + 57},
		{"Duration: 02:05:12", 100, CallCompleted, 2*3600 + 5*60 + 12},
		{"Duration: 06:13:35", 100, CallCompleted, 6*3600 + 13*60 + 35},
		{"通话中断 06:04", 100, CallInterrupted, 6*60 + 4},
		{"通话中断 01:00:19", 100, CallInterrupted, 1*3600 + 0*60 + 19},
		{"通话中断 03:02:24", 100, CallInterrupted, 3*3600 + 2*60 + 24},
		{"Already answered elsewhere", 101, CallMissed, 0},
		{"Call wasn't answered", 100, CallMissed, 0},
		{"Canceled", 100, CallCanceled, 0},
		{"Call declined", 100, CallDeclined, 0},
		{"Call ended 05:31", 100, CallCompleted, 5*60 + 31},
	}
	for _, c := range cases {
		xmlStr := voipXMLFixture(c.msgText, c.msgType, 0)
		d := Decode(50, xmlStr, `<msgsource><alnode><fr>1</fr></alnode></msgsource>`)
		if d.Kind != KindCall || d.Call == nil {
			t.Fatalf("%q: not decoded as a call: %+v", c.msgText, d)
		}
		if d.Call.Outcome != c.wantOutcome {
			t.Errorf("%q: outcome = %s, want %s", c.msgText, d.Call.Outcome, c.wantOutcome)
		}
		if d.Call.DurationS != c.wantSeconds {
			t.Errorf("%q: duration = %d, want %d", c.msgText, d.Call.DurationS, c.wantSeconds)
		}
	}
}

func TestDecodeCallEnglishZHMatrix(t *testing.T) {
	cases := []struct {
		text string
		want CallOutcome
	}{
		{"Missed", CallMissed},
		{"未接听", CallMissed},
		{"对方无应答", CallMissed},
		{"已取消", CallCanceled},
		{"Declined", CallDeclined},
		{"已拒绝", CallDeclined},
		{"对方已拒绝", CallDeclined},
		{"Busy", CallBusy},
		{"忙线", CallBusy},
	}
	for _, c := range cases {
		d := Decode(50, voipXMLFixture(c.text, 100, 0), "")
		if d.Call.Outcome != c.want {
			t.Errorf("%q: outcome = %s, want %s", c.text, d.Call.Outcome, c.want)
		}
	}
}

func TestDecodeCallVideoVoiceKeywords(t *testing.T) {
	video := Decode(50, voipXMLFixture("Video call Duration: 01:00", 100, 0), "")
	if video.Call.Media != CallVideo {
		t.Errorf("video media = %s", video.Call.Media)
	}
	voiceZH := Decode(50, voipXMLFixture("语音通话 Duration: 01:00", 100, 0), "")
	if voiceZH.Call.Media != CallVoice {
		t.Errorf("voice(zh) media = %s", voiceZH.Call.Media)
	}
	// No keyword, room_type==0 (the only value observed in real data): falls back to voice.
	fallback := Decode(50, voipXMLFixture("Duration: 01:00", 100, 0), "")
	if fallback.Call.Media != CallVoice {
		t.Errorf("fallback media = %s", fallback.Call.Media)
	}
}

func TestDecodeCallUnparseableNeverDropped(t *testing.T) {
	// Garbage XML and garbage text: must still come back as Kind call with Outcome unknown,
	// never an error and never a different Kind (docs/SPEC.md §V.6, "no drops on parse failure").
	d := Decode(50, "not xml at all, and no recognized keyword either", "")
	if d.Kind != KindCall || d.Call == nil || d.Call.Outcome != CallUnknown {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeCallDurationFieldNeverTrusted(t *testing.T) {
	// Even if some future/other build sets <duration> to a nonzero, misleading value, the
	// parser must still use the <msg> text, not <duration> (docs/FORMAT.md).
	xmlStr := `<voipmsg type="VoIPBubbleMsg"><VoIPBubbleMsg><msg><![CDATA[Canceled]]></msg><room_type>0</room_type><msg_type>100</msg_type><duration>9999</duration></VoIPBubbleMsg></voipmsg>`
	d := Decode(50, xmlStr, "")
	if d.Call.Outcome != CallCanceled || d.Call.DurationS != 0 {
		t.Fatalf("%+v", d.Call)
	}
}
