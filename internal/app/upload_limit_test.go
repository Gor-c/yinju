package app

import "testing"

func TestMaxUploadBytesDefaultIs50MB(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "")
	if got := maxUploadBytes() >> 20; got != 50 {
		t.Fatalf("default upload limit = %d MB, want 50", got)
	}
}

func TestMaxUploadBytesHonoursEnv(t *testing.T) {
	t.Setenv("MAX_UPLOAD_MB", "80")
	if got := maxUploadBytes() >> 20; got != 80 {
		t.Fatalf("MAX_UPLOAD_MB=80 gave %d MB", got)
	}
}
