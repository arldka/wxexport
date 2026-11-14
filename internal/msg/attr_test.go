package msg

import "testing"

// Real WeChat XML carries cdnthumblength/rawmd5 before length/md5; whole-name matching is required.
func TestAttrsMatchWholeName(t *testing.T) {
	d := Decode(3, `<msg><img aeskey="a" cdnthumblength="3419" length="29395" hdlength="99" md5="abc" /></msg>`, "")
	if d.Media.SizeBytes != 29395 || d.Media.MD5 != "abc" {
		t.Errorf("image: size=%d md5=%q", d.Media.SizeBytes, d.Media.MD5)
	}
	d = Decode(43, `<msg><videomsg rawmd5="RAW" md5="REAL" playlength="12"/></msg>`, "")
	if d.Media.MD5 != "REAL" || d.Media.DurationS != 12 {
		t.Errorf("video: %+v", d.Media)
	}
}
