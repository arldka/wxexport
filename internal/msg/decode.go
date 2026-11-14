package msg

import (
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
)

// Decode normalizes one message row. content and source are already-decompressed text
// (see DecodeContent). It never returns an error: anything it cannot parse becomes KindUnknown
// (for an unrecognized type) with the raw type/sub preserved, per docs/SPEC.md §V.6.
func Decode(localType int64, content, source string) Decoded {
	t, sub := SplitLocalType(localType)
	d := Decoded{Type: t, Sub: sub}
	switch t {
	case typeText:
		d.Kind = KindText
		d.Text = content
	case typeImage:
		d.Kind = KindImage
		d.Media = parseImgMedia(content, "length")
	case typeVoice:
		d.Kind = KindVoice
		d.Media = parseVoiceMedia(content)
	case typeVideo:
		d.Kind = KindVideo
		d.Media = parseVideoMedia(content)
	case typeSticker:
		d.Kind = KindSticker
		d.Media = parseImgMedia(content, "len") // calibrated: stickers use `len`, not `length`
	case typeLocation:
		d.Kind = KindLocation
		d.Location = parseLocation(content)
	case typeContact:
		d.Kind = KindContactCard
	case typeCall:
		d.Kind = KindCall
		d.Call = parseCall(content)
	case typeSystem:
		if isRecall(content) {
			d.Kind = KindRecall
		} else {
			d.Kind = KindSystem
			d.Text = systemText(content)
		}
	case typeApp:
		decodeAppMsg(&d, content)
	default:
		d.Kind = KindUnknown
	}
	return d
}

// --- media (image/voice/video/sticker) ---

// attrRe matches name="value" as a whole attribute name: it must start the string or follow
// whitespace, so "length" does not match inside "cdnthumblength" and "md5" not inside "rawmd5".
var attrRe = func(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `\s*=\s*"([^"]*)"`)
}

var (
	imgMD5Re   = attrRe("md5")
	voiceLenRe = attrRe("voicelength")
	videoLenRe = attrRe("playlength")
)

func attrInt(re *regexp.Regexp, s string) (int64, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil
}

func parseImgMedia(content, sizeAttr string) *Media {
	m := &Media{}
	if mm := imgMD5Re.FindStringSubmatch(content); mm != nil {
		m.MD5 = mm[1]
	}
	if n, ok := attrInt(attrRe(sizeAttr), content); ok {
		m.SizeBytes = n
	}
	return m
}

func parseVoiceMedia(content string) *Media {
	m := &Media{}
	if n, ok := attrInt(voiceLenRe, content); ok {
		m.DurationMS = n
	}
	return m
}

func parseVideoMedia(content string) *Media {
	m := &Media{}
	if n, ok := attrInt(videoLenRe, content); ok {
		m.DurationS = n
	}
	if mm := imgMD5Re.FindStringSubmatch(content); mm != nil {
		m.MD5 = mm[1]
	}
	return m
}

// --- location ---

var locationTag = regexp.MustCompile(`<location\s+([^>]*)/?>`)

func parseLocation(content string) *struct {
	Label    string
	Lat, Lng float64
} {
	m := locationTag.FindStringSubmatch(content)
	if m == nil {
		return nil
	}
	attrs := m[1]
	get := func(name string) string {
		if mm := attrRe(name).FindStringSubmatch(attrs); mm != nil {
			return mm[1]
		}
		return ""
	}
	lat, _ := strconv.ParseFloat(get("x"), 64)
	lng, _ := strconv.ParseFloat(get("y"), 64)
	label := get("label")
	if label == "" {
		label = get("poiname")
	}
	return &struct {
		Label    string
		Lat, Lng float64
	}{label, lat, lng}
}

// --- system / recall ---

func isRecall(content string) bool {
	return strings.Contains(content, `type="revokemsg"`) || strings.Contains(content, "recalled a message") || strings.Contains(content, "撤回")
}

var sysmsgContentTag = regexp.MustCompile(`<content>([^<]*)</content>`)

func systemText(content string) string {
	if m := sysmsgContentTag.FindStringSubmatch(content); m != nil {
		return m[1]
	}
	// Strip any remaining tags as a fallback.
	return strings.TrimSpace(stripTags(content))
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }

// --- call (type 50) ---

// voipXML mirrors the real <voipmsg> structure (docs/FORMAT.md). Fields we don't use are kept
// only for documentation; encoding/xml ignores unused siblings automatically.
type voipXML struct {
	Bubble struct {
		Msg      string `xml:"msg"`
		RoomType int    `xml:"room_type"`
		MsgType  int    `xml:"msg_type"`
	} `xml:"VoIPBubbleMsg"`
}

// Confirmed real prefixes: "Duration:" and "Call ended" (English), "通话中断" (Chinese,
// "call interrupted", still has a real duration).
var durationRe = regexp.MustCompile(`(?:Duration:\s*|Call ended\s*|通话中断\s*)(\d+):(\d{2})(?::(\d{2}))?`)

func parseDurationText(text string) (secs int64, ok bool) {
	m := durationRe.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	// Groups: either [h:mm] (2 groups) misread as mm:ss, or [h:mm:ss]. FindStringSubmatch gives
	// m[1],m[2],m[3] where m[3] is "" if there was no third segment, meaning m[1]=minutes,
	// m[2]=seconds in that case; otherwise m[1]=hours, m[2]=minutes, m[3]=seconds.
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	if m[3] == "" {
		return int64(a*60 + b), true
	}
	c, _ := strconv.Atoi(m[3])
	return int64(a*3600 + b*60 + c), true
}

func parseCall(content string) *Call {
	c := &Call{Media: CallUnknownMedia, Outcome: CallUnknown}
	msgText := content
	var roomType = -1
	var v voipXML
	if xml.Unmarshal([]byte(content), &v) == nil && v.Bubble.Msg != "" {
		msgText = v.Bubble.Msg
		roomType = v.Bubble.RoomType
	}

	switch {
	case strings.Contains(msgText, "Already answered elsewhere"):
		c.Outcome = CallMissed
	case strings.Contains(msgText, "Call wasn't answered"):
		c.Outcome = CallMissed
	case strings.Contains(msgText, "Missed") || strings.Contains(msgText, "未接听") || strings.Contains(msgText, "对方无应答"):
		c.Outcome = CallMissed
	case strings.Contains(msgText, "Canceled") || strings.Contains(msgText, "已取消"):
		c.Outcome = CallCanceled
	case strings.Contains(msgText, "Call declined") || strings.Contains(msgText, "Declined") || strings.Contains(msgText, "已拒绝") || strings.Contains(msgText, "对方已拒绝"):
		c.Outcome = CallDeclined
	case strings.Contains(msgText, "Busy") || strings.Contains(msgText, "忙线"):
		c.Outcome = CallBusy
	case strings.Contains(msgText, "通话中断"):
		if secs, ok := parseDurationText(msgText); ok {
			c.Outcome, c.DurationS = CallInterrupted, secs
		}
	default:
		if secs, ok := parseDurationText(msgText); ok {
			c.Outcome, c.DurationS = CallCompleted, secs
		}
	}

	switch {
	case strings.Contains(msgText, "Video call") || strings.Contains(msgText, "视频通话"):
		c.Media = CallVideo
	case strings.Contains(msgText, "Voice call") || strings.Contains(msgText, "语音通话") || roomType == 0:
		c.Media = CallVoice
	}
	return c
}

// --- appmsg (type 49) ---

type appmsgXML struct {
	Title  string `xml:"appmsg>title"`
	Des    string `xml:"appmsg>des"`
	URL    string `xml:"appmsg>url"`
	Type   int    `xml:"appmsg>type"`
	Attach struct {
		TotalLen int64  `xml:"totallen"`
		FileExt  string `xml:"fileext"`
	} `xml:"appmsg>appattach"`
	Refer struct {
		FromUser    string `xml:"fromusr"`
		DisplayName string `xml:"displayname"`
		Content     string `xml:"content"`
		CreateTime  int64  `xml:"createtime"`
	} `xml:"appmsg>refermsg"`
}

func decodeAppMsg(d *Decoded, content string) {
	var a appmsgXML
	_ = xml.Unmarshal([]byte(content), &a) // best effort; zero value is fine on failure

	switch d.Sub {
	case subLink:
		d.Kind = KindLink
		d.Link = &struct{ Title, URL string }{a.Title, a.URL}
	case subFile:
		d.Kind = KindFile
		d.File = &struct {
			Title, Ext string
			SizeBytes  int64
		}{a.Title, a.Attach.FileExt, a.Attach.TotalLen}
	case subQuote:
		d.Kind = KindQuote
		d.Text = a.Title
		d.Quote = &Quote{FromUser: a.Refer.FromUser, DisplayName: a.Refer.DisplayName, Text: a.Refer.Content, CreateTime: a.Refer.CreateTime}
	case subForward, subForward2:
		d.Kind = KindForward
		d.AppTitle = a.Title
	case subApplet1, subApplet2, subApplet3:
		d.Kind = KindApplet
		d.AppTitle = a.Title
	case subTransfer:
		d.Kind = KindTransfer
		d.AppTitle = a.Title
	case subRedPacket:
		d.Kind = KindRedPacket
	default:
		d.Kind = KindApp
		d.AppTitle = a.Title
	}
}
