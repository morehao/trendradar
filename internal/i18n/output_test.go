package i18n

import (
	"strings"
	"testing"
)

func TestResolveDefaultAndExplicit(t *testing.T) {
	// Empty resolves to the default language (zh).
	zh, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve(\"\") error: %v", err)
	}
	if zh.ID != DefaultLanguage {
		t.Fatalf("Resolve(\"\") = %q, want default %q", zh.ID, DefaultLanguage)
	}

	en, err := Resolve("en")
	if err != nil {
		t.Fatalf("Resolve(\"en\") error: %v", err)
	}
	if en.ID != "en" {
		t.Fatalf("Resolve(\"en\") = %q, want en", en.ID)
	}
	if en.LengthUnit != "words" {
		t.Fatalf("en length unit = %q, want words", en.LengthUnit)
	}
	if zh.LengthUnit != "汉字" {
		t.Fatalf("zh length unit = %q, want 汉字", zh.LengthUnit)
	}
}

func TestResolveRegionVariants(t *testing.T) {
	cases := map[string]string{
		"zh-CN":   "zh",
		"zh-Hant": "zh",
		"en-US":   "en",
		"en-GB":   "en",
		"  en  ":  "en",
		"：":       "", // invalid, handled below
	}
	for in, want := range cases {
		lang, err := Resolve(in)
		if want == "" {
			if err == nil {
				t.Errorf("Resolve(%q) = %q, want error", in, lang.ID)
			}
			continue
		}
		if err != nil {
			t.Errorf("Resolve(%q) error: %v", in, err)
			continue
		}
		if lang.ID != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, lang.ID, want)
		}
	}
}

func TestResolveUnsupported(t *testing.T) {
	if _, err := Resolve("fr"); err == nil {
		t.Fatal("Resolve(\"fr\") should error (unsupported)")
	}
	if _, err := Resolve("de-DE"); err == nil {
		t.Fatal("Resolve(\"de-DE\") should error (unsupported)")
	}
}

func TestSupported(t *testing.T) {
	got := Supported()
	want := []string{"en", "zh"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Supported() = %v, want %v", got, want)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"ZH":    "zh",
		"zh-CN": "zh",
		"en-US": "en",
		" En ":  "en",
		"fr":    "",
		"fr-CA": "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidTag(t *testing.T) {
	valid := []string{"zh", "en", "zh-CN", "en-US", "zh-Hant-TW"}
	for _, tag := range valid {
		if !ValidTag(tag) {
			t.Errorf("ValidTag(%q) = false, want true", tag)
		}
	}
	invalid := []string{"", "z", "zh_CN", "1", "zh-", "z h"}
	for _, tag := range invalid {
		if ValidTag(tag) {
			t.Errorf("ValidTag(%q) = true, want false", tag)
		}
	}
}

func TestRenderTextsComplete(t *testing.T) {
	for _, id := range Supported() {
		lang := MustResolve(id)
		r := lang.Render
		if r.DigestTitle == "" || r.ReleasesTitle == "" || r.TableHeader == "" || r.TableSeparator == "" ||
			r.DegradedBanner == "" || r.ReleasesDegradedBanner == "" || r.NoReleases == "" || r.ReleaseLinkLabel == "" {
			t.Errorf("%s: some RenderTexts field is empty: %+v", id, r)
		}
		for _, p := range []string{"daily", "weekly", "monthly"} {
			if r.Periods[p] == "" {
				t.Errorf("%s: Periods[%q] empty", id, p)
			}
		}
	}
}

func TestZhRenderTextsMatchLegacy(t *testing.T) {
	zh := MustResolve("zh").Render
	// Byte-identical to the pre-i18n literals (regression guard).
	if zh.TableHeader != "| 仓库 | Stars | 今日新增 | 摘要 |" {
		t.Errorf("zh table header changed: %q", zh.TableHeader)
	}
	if zh.DigestTitle != "趋势摘要" || zh.ReleasesTitle != "发版摘要" || zh.ReleasesIndexTitle != "发版索引" {
		t.Errorf("zh titles changed: %+v", zh)
	}
}
