package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIHelpAndUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"-h"}, &out, &errOut); code != 0 || !strings.Contains(errOut.String(), "打开界面") {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"foo"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "未知命令") {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"ota"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "缺少 --host") {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"frame"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "缺少 --host") {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"export"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "用法") {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &errOut)
	}
}
