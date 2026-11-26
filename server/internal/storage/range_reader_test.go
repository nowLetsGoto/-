package storage

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// rangeServer 用内存字节模拟支持 Range 的 HTTP 服务，并记录请求次数以验证缓存效果。
func rangeServer(data []byte, requests *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		rng := r.Header.Get("Range")
		if rng == "" {
			w.Write(data)
			return
		}
		// 解析 "bytes=start-end"。
		spec := strings.TrimPrefix(rng, "bytes=")
		parts := strings.SplitN(spec, "-", 2)
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		if start >= len(data) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if end >= len(data) {
			end = len(data) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[start : end+1])
	}))
}

func TestHTTPRangeReaderReadAt(t *testing.T) {
	// 造 3 个块的数据，覆盖跨块读取与末尾不足一块两种情况。
	data := bytes.Repeat([]byte("abcdefgh"), rangeBlockSize/8*2+100)
	var requests int
	srv := rangeServer(data, &requests)
	defer srv.Close()

	r := NewHTTPRangeReader(srv.URL, HTTPClientForRange())

	cases := []struct {
		off  int64
		size int
	}{
		{0, 16},                      // 块 0 开头
		{rangeBlockSize - 8, 32},     // 跨块 0 → 1
		{int64(len(data)) - 50, 100}, // 末尾越界，应返回 io.EOF
	}
	for _, c := range cases {
		got := make([]byte, c.size)
		n, err := r.ReadAt(got, c.off)
		want := data[c.off:min(int(c.off)+c.size, len(data))]
		if !bytes.Equal(got[:n], want) {
			t.Fatalf("ReadAt(off=%d,size=%d) 内容不符: got %q want %q", c.off, c.size, got[:n], want)
		}
		if c.off+int64(c.size) > int64(len(data)) && err != io.EOF {
			t.Fatalf("越界读取应返回 EOF, got %v", err)
		}
	}

	// 4 个块（数据在 2.x 块处结束），请求数应等于块数，重复读取不新增请求。
	before := requests
	for _, c := range cases {
		r.ReadAt(make([]byte, c.size), c.off)
	}
	if requests != before {
		t.Fatalf("缓存命中不应新增请求: before=%d after=%d", before, requests)
	}
}

func TestHTTPRangeReaderRejectsNegativeOffset(t *testing.T) {
	r := NewHTTPRangeReader("http://unused", HTTPClientForRange())
	if _, err := r.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("负偏移应返回错误")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
