package app

import "testing"

// 上传的 logo 只按魔数判断格式。扩展名与 Content-Type 都由客户端提供，
// 改个后缀就能把任意内容塞进数据目录，所以这里必须逐种格式验证。
func TestImageExtUsesMagicBytes(t *testing.T) {
	cases := []struct {
		name    string
		head    []byte
		wantExt string
		wantOK  bool
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), ".png", true},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}, ".jpg", true},
		{"webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 "), ".webp", true},
		{"svg 被拒", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "", false},
		{"改后缀的 html", []byte("<!doctype html><title>x</title>"), "", false},
		{"纯文本", []byte("this is not an image at all"), "", false},
		{"头部不足", []byte("\x89PN"), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext, ok := imageExt(tc.head)
			if ok != tc.wantOK || ext != tc.wantExt {
				t.Fatalf("imageExt() = (%q, %v), want (%q, %v)", ext, ok, tc.wantExt, tc.wantOK)
			}
		})
	}
}

// 名称是唯一会被塞进页面和标签页的自由文本，长度必须被截断，
// 否则一个超长名称会把侧栏布局撑坏。
func TestBrandPayloadTruncation(t *testing.T) {
	long := make([]rune, 0, 200)
	for i := 0; i < 200; i++ {
		long = append(long, '名')
	}
	// truncateRunes 是 brand 保存路径上实际使用的截断函数。
	if got := truncateRunes(string(long), brandNameMaxRunes); len([]rune(got)) != brandNameMaxRunes {
		t.Fatalf("truncateRunes 长度 = %d, want %d", len([]rune(got)), brandNameMaxRunes)
	}
}
