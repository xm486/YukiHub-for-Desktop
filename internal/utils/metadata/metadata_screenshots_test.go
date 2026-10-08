package metadata

import (
	"encoding/json"
	"reflect"
	"testing"
)

// 大屏详情层的 INTRODUCTION 画带要用的截图来源（对齐手机端 M2/§S3）：
// VNDB screenshots[]、Hikarinagi images[]、NextMoe screenshots[] —— 每源取前 2 张。
// 这里钉住「JSON 键名 + 取值顺序 + 上限」，避免以后改结构体标签时静默失效，
// 表现为大屏画带突然空了却没有任何报错。

func TestNormalizeMetadataScreenshotsDedupesTrimsAndCaps(t *testing.T) {
	got := normalizeMetadataScreenshots([]string{
		"  https://a/1.jpg  ",
		"",
		"https://a/1.jpg", // 重复
		"https://a/2.jpg",
		"https://a/3.jpg", // 超出每源上限
	})
	want := []string{"https://a/1.jpg", "https://a/2.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeMetadataScreenshots = %v, want %v", got, want)
	}

	if normalizeMetadataScreenshots(nil) != nil {
		t.Fatal("空输入应返回 nil")
	}
	if normalizeMetadataScreenshots([]string{"", "   "}) != nil {
		t.Fatal("全空输入应返回 nil")
	}
}

func TestVNDBScreenshotsPreferThumbnail(t *testing.T) {
	var result vndbQueryResult
	payload := `{"id":"v1","screenshots":[
		{"url":"https://t.vndb.org/sf/1.jpg","thumbnail":"https://t.vndb.org/st/1.jpg"},
		{"url":"https://t.vndb.org/sf/2.jpg","thumbnail":""},
		{"url":"https://t.vndb.org/sf/3.jpg","thumbnail":"https://t.vndb.org/st/3.jpg"}
	]}`
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("解析 VNDB 响应失败: %v", err)
	}

	want := []string{"https://t.vndb.org/st/1.jpg", "https://t.vndb.org/sf/2.jpg"}
	if got := vndbScreenshotURLs(result.Screenshots); !reflect.DeepEqual(got, want) {
		t.Fatalf("vndbScreenshotURLs = %v, want %v（thumbnail 优先、缺省回退 url、每源 2 张）", got, want)
	}
}

func TestHikarinagiScreenshotsUseImages(t *testing.T) {
	var game hikarinagiGame
	payload := `{"id":14792,"images":[
		{"url":"https://api.hikarinagi.com/storage/1.webp"},
		{"url":"https://api.hikarinagi.com/storage/2.webp"},
		{"url":"https://api.hikarinagi.com/storage/3.webp"}
	]}`
	if err := json.Unmarshal([]byte(payload), &game); err != nil {
		t.Fatalf("解析 Hikarinagi 响应失败: %v", err)
	}

	want := []string{
		"https://api.hikarinagi.com/storage/1.webp",
		"https://api.hikarinagi.com/storage/2.webp",
	}
	if got := hikarinagiScreenshotURLs(game.Images); !reflect.DeepEqual(got, want) {
		t.Fatalf("hikarinagiScreenshotURLs = %v, want %v", got, want)
	}
}

func TestNextMoeScreenshotsUseScreenshotsArray(t *testing.T) {
	var work nextMoeWork
	payload := `{"id":"nm1","screenshots":[
		{"url":"https://nextmoe.example/s1.jpg"},
		{"url":"https://nextmoe.example/s2.jpg"},
		{"url":"https://nextmoe.example/s3.jpg"}
	]}`
	if err := json.Unmarshal([]byte(payload), &work); err != nil {
		t.Fatalf("解析 NextMoe 响应失败: %v", err)
	}

	want := []string{"https://nextmoe.example/s1.jpg", "https://nextmoe.example/s2.jpg"}
	if got := nextMoeScreenshotURLs(work.Screenshots); !reflect.DeepEqual(got, want) {
		t.Fatalf("nextMoeScreenshotURLs = %v, want %v", got, want)
	}
}
