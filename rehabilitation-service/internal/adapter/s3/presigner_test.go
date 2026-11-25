package s3

import "testing"

func TestObjectKey(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		bucket  string
		want    string
		wantErr bool
	}{
		{
			name:   "yandex style",
			url:    "https://storage.yandexcloud.net/saymo-programs/programs/p1/lessons/l1/v1.pdf",
			bucket: "saymo-programs",
			want:   "programs/p1/lessons/l1/v1.pdf",
		},
		{
			name:   "path style with port",
			url:    "http://localhost:9005/saymo-programs/programs/p1/lessons/l1/v1.pdf",
			bucket: "saymo-programs",
			want:   "programs/p1/lessons/l1/v1.pdf",
		},
		{
			name:    "wrong bucket",
			url:     "https://cdn.example/other-bucket/file.pdf",
			bucket:  "saymo-programs",
			wantErr: true,
		},
		{
			name:    "empty key",
			url:     "https://storage.yandexcloud.net/saymo-programs/",
			bucket:  "saymo-programs",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := objectKey(tc.url, tc.bucket)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}
