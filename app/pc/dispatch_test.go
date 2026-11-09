package main

import "testing"

func TestLaunchGUI(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		open  bool
		image string
	}{
		{"无参数", nil, true, ""},
		{"png", []string{"照片.PNG"}, true, "照片.PNG"},
		{"jpeg 路径", []string{`C:\img\a.jpeg`}, true, `C:\img\a.jpeg`},
		{"jpg", []string{"a.jpg"}, true, "a.jpg"},
		{"ota", []string{"ota"}, false, ""},
		{"frame", []string{"frame"}, false, ""},
		{"export", []string{"export"}, false, ""},
		{"帮助", []string{"-h"}, false, ""},
		{"带 host", []string{"--host", "192.168.1.2", "ota", "status"}, false, ""},
		{"未知词", []string{"foo"}, false, ""},
		{"固件", []string{"LumenFolio.bin"}, false, ""},
		{"图片加多余参数", []string{"a.png", "b.png"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			open, image := launchGUI(tc.args)
			if open != tc.open || image != tc.image {
				t.Fatalf("launchGUI(%q) = %v %q，应为 %v %q", tc.args, open, image, tc.open, tc.image)
			}
		})
	}
}
