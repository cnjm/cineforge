package service

// 剧本文件解析与校验（对齐 legacy backend/app/api/routes/projects.py 的
// _extract_script_text / _validate_script_upload / _validate_script_text /
// _validate_single_episode_script / _parser_name / _detect_script_language）。
// docx 使用 Go 原生 archive/zip + encoding/xml，安全限制与错误文案逐字一致。

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	// MaxScriptFileSize 剧本文件大小上限（同 legacy MAX_SCRIPT_FILE_SIZE，413）。
	MaxScriptFileSize = 20 << 20
	// MaxScriptTextChars 剧本正文上限（2,000,000 字符）。
	MaxScriptTextChars = 2_000_000
	// MaxDocxEntries / MaxDocxUncompressedSize / MaxDocxXMLSize docx 安全限制。
	MaxDocxEntries           = 10_000
	MaxDocxUncompressedSize  = 50 << 20
	MaxDocxXMLSize           = 10 << 20
	// ScriptCharsValidationWindow 控制字符抽样窗口（同 legacy 100_000）。
	ScriptCharsValidationWindow = 100_000
)

var (
	ErrUnsupportedExt      = errors.New("剧本导入仅支持 .txt、.md、.docx 文件。")
	ErrFileTooLarge        = errors.New("剧本文件不能超过 20MB。")
	ErrDocxTooManyEntries  = errors.New("DOCX 文件包含过多内部条目，已拒绝解析。")
	ErrDocxUnsafePath      = errors.New("DOCX 文件包含不安全的内部路径。")
	ErrDocxTooLarge        = errors.New("DOCX 解压后内容超过 50MB 限制。")
	ErrDocxXMLTooLarge     = errors.New("DOCX 正文结构超过 10MB 限制。")
	ErrDocxInvalid         = errors.New("DOCX 文件结构无效或缺少 word/document.xml。")
	ErrEncodingUnrecognized = errors.New("文本文件编码无法识别，请使用 UTF-8 或 GB18030 编码。")
	ErrEmptyScriptText     = errors.New("剧本文件未解析出有效文本，请检查文件内容或格式。")
	ErrScriptTooLong       = errors.New("剧本正文不能超过 200 万字符。")
	ErrControlChars        = errors.New("剧本文本包含过多无效控制字符，请检查文件编码或内容。")
	ErrUploadRequired      = errors.New("请上传剧本文件后再导入。")
)

var allowedScriptExtensions = map[string]bool{".txt": true, ".md": true, ".docx": true}

func fileSuffix(filename string) string {
	if idx := strings.LastIndexByte(filename, '.'); idx >= 0 && idx < len(filename)-1 {
		return strings.ToLower(filename[idx:])
	}
	return ""
}

// ValidateScriptUpload 校验扩展名与文件大小（400 / 413，文案逐字）。
func ValidateScriptUpload(filename string, raw []byte) error {
	if !allowedScriptExtensions[fileSuffix(filename)] {
		return ErrUnsupportedExt
	}
	if len(raw) > MaxScriptFileSize {
		return ErrFileTooLarge
	}
	return nil
}

// ExtractScriptText 按扩展名解析剧本正文；docx 走 XML，其余走文本编码。
func ExtractScriptText(filename string, raw []byte) (string, error) {
	if fileSuffix(filename) == ".docx" {
		return extractDocxText(raw)
	}
	for _, dec := range []func([]byte) (string, error){decodeUTF8Signature, decodeGB18030} {
		if text, err := dec(raw); err == nil {
			return text, nil
		}
	}
	return "", ErrEncodingUnrecognized
}

func decodeUTF8Signature(raw []byte) (string, error) {
	const utf8BOM = "\xef\xbb\xbf"
	body := raw
	if bytes.HasPrefix(body, []byte(utf8BOM)) {
		body = body[len(utf8BOM):]
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("invalid utf-8")
	}
	return string(body), nil
}

func decodeGB18030(raw []byte) (string, error) {
	out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var docxNS = map[string]string{"w": "http://schemas.openxmlformats.org/wordprocessingml/2006/main"}

func extractDocxText(raw []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", ErrDocxInvalid
	}
	if len(reader.File) > MaxDocxEntries {
		return "", ErrDocxTooManyEntries
	}
	var totalSize uint64
	var document *zip.File
	for _, f := range reader.File {
		if hasUnsafeZipPath(f.Name) {
			return "", ErrDocxUnsafePath
		}
		totalSize += f.UncompressedSize64
		if f.Name == "word/document.xml" {
			document = f
		}
	}
	if totalSize > MaxDocxUncompressedSize {
		return "", ErrDocxTooLarge
	}
	if document == nil {
		return "", ErrDocxInvalid
	}
	if document.UncompressedSize64 > MaxDocxXMLSize {
		return "", ErrDocxXMLTooLarge
	}
	rc, err := document.Open()
	if err != nil {
		return "", ErrDocxInvalid
	}
	defer rc.Close()
	xmlBytes, err := io.ReadAll(io.LimitReader(rc, MaxDocxXMLSize+1))
	if err != nil {
		return "", ErrDocxInvalid
	}
	if len(xmlBytes) > MaxDocxXMLSize {
		return "", ErrDocxXMLTooLarge
	}
	root := &docxDocument{}
	if err := xml.Unmarshal(xmlBytes, root); err != nil {
		return "", ErrDocxInvalid
	}
	var paragraphs []string
	for _, p := range root.Paragraphs {
		var sb strings.Builder
		for _, t := range p.Texts {
			sb.WriteString(t.Text)
		}
		if trimmed := strings.TrimSpace(sb.String()); trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
	}
	return collapseNewlines(strings.Join(paragraphs, "\n")), nil
}

// hasUnsafeZipPath 对齐 legacy：路径以 / 或 \ 开头，或含任意 ".." 段视为不安全。
func hasUnsafeZipPath(name string) bool {
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return true
	}
	for _, seg := range strings.Split(strings.ReplaceAll(name, "\\", "/"), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// collapseNewlines 对齐 legacy re.sub(r"\n{3,}", "\n\n", text)。
func collapseNewlines(text string) string {
	var sb strings.Builder
	newlineRun := 0
	for _, r := range text {
		if r == '\n' {
			newlineRun++
			if newlineRun <= 2 {
				sb.WriteRune(r)
			}
			continue
		}
		newlineRun = 0
		sb.WriteRune(r)
	}
	return sb.String()
}

// docxDocument 解析 wordprocessingml 主命名空间的最小片段。
type docxDocument struct {
	XMLName    xml.Name        `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main document"`
	Paragraphs []docxParagraph `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main p"`
}

type docxParagraph struct {
	Texts []docxText `xml:"http://schemas.openxmlformats.org/wordprocessingml/2006/main t"`
}

type docxText struct {
	Text string `xml:",chardata"`
}

// ValidateScriptText 校验正文长度与控制字符占比。
func ValidateScriptText(scriptText string) error {
	if len([]rune(scriptText)) > MaxScriptTextChars {
		return ErrScriptTooLong
	}
	sample := scriptText
	runes := []rune(scriptText)
	if len(runes) > ScriptCharsValidationWindow {
		sample = string(runes[:ScriptCharsValidationWindow])
	}
	if sample == "" {
		return nil
	}
	meaningful := 0
	for _, r := range sample {
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			meaningful++
		}
	}
	// legacy 用 Python len(str)（字符数）比较；Go 的 len(string) 是字节数，
	// CJK 每字 3 字节会把任何中文文本误判为非打印 → 按 rune 数比较。
	if meaningful*10 < len([]rune(sample))*9 {
		return ErrControlChars
	}
	return nil
}

// ParserName 对齐 legacy _parser_name。
func ParserName(filename string) string {
	switch fileSuffix(filename) {
	case ".docx":
		return "docx_xml_v1"
	case ".md":
		return "markdown_text_v1"
	default:
		return "plain_text_v1"
	}
}

// DetectScriptLanguage 对齐 legacy _detect_script_language。
func DetectScriptLanguage(scriptText string) string {
	text := strings.TrimSpace(scriptText)
	if text == "" {
		return "unknown"
	}
	cjkCount, latinCount := 0, 0
	for _, r := range text {
		if r >= '一' && r <= '鿿' {
			cjkCount++
		} else if r < utf8.RuneSelf && unicode.IsLetter(r) { // isascii && isalpha
			latinCount++
		}
	}
	if cjkCount > 0 && latinCount > 0 {
		if cjkCount >= latinCount/3 {
			return "zh-mixed"
		}
		return "multilingual"
	}
	if cjkCount > 0 {
		return "zh"
	}
	if latinCount > 0 {
		return "latin"
	}
	return "unknown"
}

var _episodeHeaderRe = regexp.MustCompile(
	`^\s*(?:第\s*([〇零一二两三四五六七八九十百\d]+)\s*[集话]|EP(?:ISODE)?\s*[-_：:]?\s*0*(\d+))\s*(?:[：:\-—_\s].*)?$`)

var chineseDigits = map[rune]int{'〇': 0, '零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// ChineseEpisodeNumber 对齐 legacy _chinese_episode_number（支持 百/十 组合）。
func ChineseEpisodeNumber(value string) (int, bool) {
	normalized := strings.TrimSpace(value)
	if n, err := strconv.Atoi(normalized); err == nil {
		return n, true
	}
	runes := []rune(normalized)
	if idx := indexRune(runes, '百'); idx >= 0 {
		left := runes[:idx]
		right := runes[idx+1:]
		hundreds := 1
		if len(left) > 0 {
			v, ok := chineseDigits[left[0]]
			if !ok {
				v = 1
			}
			hundreds = v
		}
		tail := 0
		if len(right) > 0 {
			if v, ok := ChineseEpisodeNumber(string(right)); ok {
				tail = v
			}
		}
		return hundreds*100 + tail, true
	}
	if idx := indexRune(runes, '十'); idx >= 0 {
		left := runes[:idx]
		right := runes[idx+1:]
		tens := 1
		if len(left) > 0 {
			if v, ok := chineseDigits[left[0]]; ok {
				tens = v
			}
		}
		ones := 0
		if len(right) > 0 {
			if v, ok := chineseDigits[right[0]]; ok {
				ones = v
			}
		}
		return tens*10 + ones, true
	}
	if len(runes) == 1 {
		if v, ok := chineseDigits[runes[0]]; ok {
			return v, true
		}
	}
	return 0, false
}

func indexRune(runes []rune, target rune) int {
	for i, r := range runes {
		if r == target {
			return i
		}
	}
	return -1
}

// ScriptEpisodeNumbers 提取剧本中声明的分集编号（去重去 0）。
func ScriptEpisodeNumbers(scriptText string) []int {
	var numbers []int
	for _, line := range strings.Split(scriptText, "\n") {
		m := _episodeHeaderRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var n int
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		} else if v, ok := ChineseEpisodeNumber(m[1]); ok {
			n = v
		}
		if n > 0 {
			dup := false
			for _, existing := range numbers {
				if existing == n {
					dup = true
					break
				}
			}
			if !dup {
				numbers = append(numbers, n)
			}
		}
	}
	return numbers
}

// ValidateSingleEpisodeScript 对齐 legacy：多个分集标题 400，编号与目标不符 400。
func ValidateSingleEpisodeScript(scriptText string, targetEpisodeNo int) error {
	numbers := ScriptEpisodeNumbers(scriptText)
	if len(numbers) > 1 {
		labels := make([]string, 0, len(numbers))
		for _, n := range numbers {
			labels = append(labels, fmt.Sprintf("EP%02d", n))
		}
		return fmt.Errorf("检测到多个分集标题（%s），请拆分为单集文件后分别导入。", strings.Join(labels, "、"))
	}
	if len(numbers) == 1 && numbers[0] != targetEpisodeNo {
		return fmt.Errorf("剧本标注为 EP%02d，当前目标为 EP%02d，请确认分集后重新上传。", numbers[0], targetEpisodeNo)
	}
	return nil
}