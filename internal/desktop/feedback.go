package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const feedbackEndpoint = "https://phptools.wuyouzhilian.cn/api/feedback.php"
const officialSiteURL = "https://phptools.wuyouzhilian.cn/"
const feedbackPageURL = "https://github.com/vonch/phptools/issues/new/choose"
const feedbackImageLimit = 3
const feedbackImageInputBytes = 8 * 1024 * 1024
const feedbackImageOutputBytes = 1024 * 1024
const feedbackImageMaxPixels = 8_000_000

// FeedbackImage is one screenshot supplied by the app. Data is standard base64.
type FeedbackImage struct {
	Data string `json:"data"`
}

// FeedbackReport is the template shared by the app and the website.
type FeedbackReport struct {
	Kind     string          `json:"kind"`
	Title    string          `json:"title"`
	Version  string          `json:"version"`
	Steps    string          `json:"steps"`
	Expected string          `json:"expected"`
	Actual   string          `json:"actual"`
	Problem  string          `json:"problem"`
	Proposal string          `json:"proposal"`
	Contact  string          `json:"contact"`
	Images   []FeedbackImage `json:"images"`
}

type feedbackResponse struct {
	OK    bool   `json:"ok"`
	ID    string `json:"id"`
	Error string `json:"error"`
}

// OpenOfficialSite opens the product website in the system browser.
func (a *App) OpenOfficialSite() error {
	if a.hooks.OpenURL == nil {
		return errors.New("浏览器打开功能尚未初始化")
	}
	return a.hooks.OpenURL(officialSiteURL)
}

// OpenFeedback opens the GitHub issue chooser for a suggestion or bug report.
func (a *App) OpenFeedback() error {
	if a.hooks.OpenURL == nil {
		return errors.New("浏览器打开功能尚未初始化")
	}
	return a.hooks.OpenURL(feedbackPageURL)
}

// SubmitFeedback sends a suggestion or bug report to the official website.
func (a *App) SubmitFeedback(report FeedbackReport) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id, err := submitFeedback(ctx, a.updateTransport(45*time.Second), feedbackEndpoint, report)
	if err != nil {
		return "", redactUpdateAddress(err)
	}
	return id, nil
}

func submitFeedback(ctx context.Context, client *http.Client, endpoint string, report FeedbackReport) (string, error) {
	payload, err := normalizeFeedback(report)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("反馈内容无法发送")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("反馈内容无法发送")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8192))
	if err != nil {
		return "", errors.New("没有收到反馈结果")
	}
	var decoded feedbackResponse
	if json.Unmarshal(raw, &decoded) != nil {
		return "", errors.New("提交没有成功，请稍后再试。")
	}
	if decoded.Error != "" {
		return "", feedbackServerError(decoded.Error)
	}
	if response.StatusCode != http.StatusOK || !decoded.OK || !feedbackIDPattern(decoded.ID) {
		return "", errors.New("提交没有成功，请稍后再试。")
	}
	return decoded.ID, nil
}

func normalizeFeedback(report FeedbackReport) (FeedbackReport, error) {
	var err error
	if report.Kind != "bug" && report.Kind != "suggestion" {
		return FeedbackReport{}, errors.New("请选择建议或 BUG。")
	}
	limits := map[string]int{
		"title": 60, "version": 80, "steps": 2000, "expected": 2000,
		"actual": 2000, "problem": 2000, "proposal": 2000, "contact": 80,
	}
	report.Title, err = feedbackText(report.Title, limits["title"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Version, err = feedbackText(report.Version, limits["version"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Steps, err = feedbackText(report.Steps, limits["steps"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Expected, err = feedbackText(report.Expected, limits["expected"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Actual, err = feedbackText(report.Actual, limits["actual"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Problem, err = feedbackText(report.Problem, limits["problem"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Proposal, err = feedbackText(report.Proposal, limits["proposal"])
	if err != nil {
		return FeedbackReport{}, err
	}
	report.Contact, err = feedbackText(report.Contact, limits["contact"])
	if err != nil {
		return FeedbackReport{}, err
	}
	if utf8.RuneCountInString(report.Title) < 2 {
		return FeedbackReport{}, errors.New("请用一句话写清标题。")
	}
	if report.Kind == "bug" {
		if utf8.RuneCountInString(report.Steps) < 4 || utf8.RuneCountInString(report.Expected) < 4 || utf8.RuneCountInString(report.Actual) < 4 {
			return FeedbackReport{}, errors.New("BUG 需要写清操作步骤、期望结果和实际结果。")
		}
	} else if utf8.RuneCountInString(report.Problem) < 4 || utf8.RuneCountInString(report.Proposal) < 4 {
		return FeedbackReport{}, errors.New("建议需要写清现在的问题和希望的做法。")
	}
	if len(report.Images) > feedbackImageLimit {
		return FeedbackReport{}, fmt.Errorf("最多上传 %d 张图片。", feedbackImageLimit)
	}
	images := make([]FeedbackImage, 0, len(report.Images))
	for _, image := range report.Images {
		data := strings.TrimSpace(image.Data)
		if data == "" {
			continue
		}
		if comma := strings.Index(data, ","); strings.HasPrefix(data, "data:") && comma >= 0 {
			data = data[comma+1:]
		}
		if len(data) > feedbackImageInputBytes*4/3+16 {
			return FeedbackReport{}, errors.New("单张原图不能超过 8 MB。")
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(data)
		if decodeErr != nil {
			return FeedbackReport{}, errors.New("有一张图片无法读取。")
		}
		prepared, imageErr := prepareFeedbackImage(decoded)
		if imageErr != nil {
			return FeedbackReport{}, imageErr
		}
		images = append(images, FeedbackImage{Data: base64.StdEncoding.EncodeToString(prepared)})
	}
	report.Images = images
	return report, nil
}

func feedbackText(value string, limit int) (string, error) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var cleaned strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\t' || (r >= 32 && r != 0x7f) {
			cleaned.WriteRune(r)
		}
	}
	value = strings.TrimSpace(cleaned.String())
	if utf8.RuneCountInString(value) > limit {
		return "", fmt.Errorf("有一段内容超过 %d 个字。", limit)
	}
	return value, nil
}

func prepareFeedbackImage(data []byte) ([]byte, error) {
	if len(data) > feedbackImageInputBytes {
		return nil, errors.New("单张原图不能超过 8 MB。")
	}
	if _, err := feedbackImageExt(data); err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 8000 || config.Height > 8000 || config.Width*config.Height > feedbackImageMaxPixels {
		return nil, errors.New("图片尺寸过大或无法读取。")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("有一张图片无法读取。")
	}
	fitted := fitFeedbackImage(decoded, 1920)
	quality := 85
	for attempt := 0; attempt < 12; attempt++ {
		var encoded bytes.Buffer
		if err = jpeg.Encode(&encoded, fitted, &jpeg.Options{Quality: quality}); err != nil {
			return nil, errors.New("暂时无法处理图片。")
		}
		if encoded.Len() <= feedbackImageOutputBytes && bytes.HasPrefix(encoded.Bytes(), []byte{0xFF, 0xD8, 0xFF}) {
			return encoded.Bytes(), nil
		}
		if quality > 55 {
			quality -= 10
			continue
		}
		edge := fitted.Bounds().Dx()
		if fitted.Bounds().Dy() > edge {
			edge = fitted.Bounds().Dy()
		}
		nextEdge := int(math.Floor(float64(edge) * 0.75))
		if nextEdge < 480 {
			break
		}
		fitted = fitFeedbackImage(fitted, nextEdge)
		quality = 75
	}
	return nil, errors.New("这张图片无法压到 1 MB 以内。")
}

func fitFeedbackImage(src image.Image, maxEdge int) *image.NRGBA {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	targetWidth, targetHeight := width, height
	edge := width
	if height > edge {
		edge = height
	}
	if edge > maxEdge && edge > 0 {
		scale := float64(maxEdge) / float64(edge)
		targetWidth = int(math.Round(float64(width) * scale))
		targetHeight = int(math.Round(float64(height) * scale))
		if targetWidth < 1 {
			targetWidth = 1
		}
		if targetHeight < 1 {
			targetHeight = 1
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sy0 := bounds.Min.Y + y*height/targetHeight
		sy1 := bounds.Min.Y + (y+1)*height/targetHeight
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < targetWidth; x++ {
			sx0 := bounds.Min.X + x*width/targetWidth
			sx1 := bounds.Min.X + (x+1)*width/targetWidth
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var red, green, blue, count uint32
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, b, _ := src.At(sx, sy).RGBA()
					red += r
					green += g
					blue += b
					count++
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8((red / count) >> 8),
				G: uint8((green / count) >> 8),
				B: uint8((blue / count) >> 8),
				A: 255,
			})
		}
	}
	return dst
}

func feedbackImageExt(data []byte) (string, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "jpg", nil
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "png", nil
	case len(data) >= 6 && (bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))):
		return "gif", nil
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "webp", nil
	default:
		return "", errors.New("图片只接受 JPG、PNG、GIF 或 WebP。")
	}
}

func feedbackServerError(message string) error {
	message = strings.TrimSpace(message)
	if message == "" || utf8.RuneCountInString(message) > 200 || strings.Contains(strings.ToLower(message), "http") || strings.ContainsAny(message, "<>") {
		return errors.New("提交没有成功，请稍后再试。")
	}
	for _, r := range message {
		if r < 32 && r != '\n' && r != '\t' {
			return errors.New("提交没有成功，请稍后再试。")
		}
	}
	return errors.New(message)
}

func feedbackIDPattern(id string) bool {
	if len(id) != 8+1+12 {
		return false
	}
	for i, r := range id {
		switch {
		case i < 8 && r >= '0' && r <= '9':
		case i == 8 && r == '-':
		case i > 8 && ((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')):
		default:
			return false
		}
	}
	return true
}
