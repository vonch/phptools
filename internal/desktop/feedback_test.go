package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenOfficialSite(t *testing.T) {
	a := testApp(t)
	if err := a.OpenOfficialSite(); err == nil {
		t.Fatal("expected missing browser hook to fail")
	}
	var opened string
	a.hooks.OpenURL = func(url string) error {
		opened = url
		return nil
	}
	if err := a.OpenOfficialSite(); err != nil {
		t.Fatal(err)
	}
	if opened != officialSiteURL {
		t.Fatalf("opened %s", opened)
	}
}

func TestSubmitFeedback(t *testing.T) {
	var got FeedbackReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"id":"20261107-abc123def456"}`))
	}))
	defer server.Close()

	id, err := submitFeedback(t.Context(), server.Client(), server.URL, FeedbackReport{
		Kind:     "bug",
		Title:    "站点打不开",
		Version:  "0.1.32",
		Steps:    "1. 打开站点页\n2. 点击访问",
		Expected: "浏览器打开本地域名",
		Actual:   "没有反应",
		Images:   []FeedbackImage{{Data: "data:image/png;base64," + tinyPNG(t)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "20261107-abc123def456" {
		t.Fatalf("id %s", id)
	}
	if got.Kind != "bug" || len(got.Images) != 1 || strings.Contains(got.Images[0].Data, "data:") {
		t.Fatalf("payload %+v", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Images[0].Data)
	if err != nil || !bytes.HasPrefix(decoded, []byte{0xFF, 0xD8, 0xFF}) || bytes.Contains(decoded, []byte("<?php")) {
		t.Fatalf("image was not re-encoded: %v", err)
	}

	_, err = submitFeedback(t.Context(), server.Client(), server.URL, FeedbackReport{Kind: "suggestion", Title: "好", Problem: "现在要点很多下", Proposal: "希望可以直接启动"})
	if err == nil || !strings.Contains(err.Error(), "标题") {
		t.Fatalf("short title error %v", err)
	}
	_, err = submitFeedback(t.Context(), server.Client(), server.URL, FeedbackReport{
		Kind: "bug", Title: "标题够了", Steps: "1234", Expected: "1234", Actual: "1234",
		Images: []FeedbackImage{{Data: base64.StdEncoding.EncodeToString([]byte("not-an-image"))}},
	})
	if err == nil || !strings.Contains(err.Error(), "图片") {
		t.Fatalf("bad image error %v", err)
	}
}

func TestFeedbackServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"ok":false,"error":"提交过于频繁，请一小时后再试。"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := submitFeedback(t.Context(), server.Client(), server.URL, FeedbackReport{
		Kind: "suggestion", Title: "启动太慢", Problem: "每次都要手动点启动", Proposal: "希望安装后直接启动",
	})
	if err == nil || err.Error() != "提交过于频繁，请一小时后再试。" {
		t.Fatalf("error %v", err)
	}
}

func tinyPNG(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	buf.WriteString("<?php echo 1;")
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
