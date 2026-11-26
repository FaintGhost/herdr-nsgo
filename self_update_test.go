package main

import "testing"

func TestParseAndroidAppcastAndSemVer(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>herdr-nsgo</title>
    <item>
      <title>herdr-nsgo 1.0.4</title>
      <link>https://github.com/FaintGhost/herdr-nsgo/releases/tag/v1.0.4</link>
      <description>&lt;p&gt;Fix Claude &amp;amp; Agy effort display&lt;/p&gt;</description>
      <sparkle:version>1.0.4</sparkle:version>
      <sparkle:shortVersionString>1.0.4</sparkle:shortVersionString>
      <enclosure url="https://github.com/FaintGhost/herdr-nsgo/releases/download/v1.0.4/herdr-nsgo-1.0.4-android-arm64.apk" length="11600000" type="application/octet-stream" sparkle:os="android" sparkle:version="1.0.4" sparkle:shortVersionString="1.0.4" sparkle:sha256="deadbeef" sparkle:edSignature="sig123=="/>
    </item>
  </channel>
</rss>`)

	chk, err := parseAndroidAppcast(xmlData, "1.0.0")
	if err != nil {
		t.Fatalf("parseAndroidAppcast error: %v", err)
	}
	if chk.Version != "1.0.4" || chk.SHA256 != "deadbeef" || chk.Signature != "sig123==" || chk.Size != 11600000 {
		t.Fatalf("unexpected UpdateCheck: %+v", chk)
	}
	if chk.Key != appUpdatePublicKey {
		t.Fatalf("expected embedded Ed25519 public key, got %q", chk.Key)
	}
	if compareSemVer(chk.Version, "1.0.0") <= 0 {
		t.Fatal("expected 1.0.4 > 1.0.0")
	}
	if compareSemVer("1.0.4", "1.0.4") != 0 {
		t.Fatal("expected 1.0.4 == 1.0.4")
	}
	if compareSemVer("1.0.10", "1.0.4") <= 0 {
		t.Fatal("expected 1.0.10 > 1.0.4")
	}
}
