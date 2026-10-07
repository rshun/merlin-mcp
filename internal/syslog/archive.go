package syslog

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// 单个归档文件解压后最多读取 32MB，防止异常文件占满内存。
const maxArchiveBytes = 32 << 20

// ArchivePath 返回本地归档目录中某一天的日志文件路径：merlin-syslog-YYYY-MM-DD.log.gz。
func ArchivePath(dir string, day time.Time) string {
	return filepath.Join(dir, "merlin-syslog-"+day.Format("2006-01-02")+".log.gz")
}

// ReadArchive 读取并解析一个 gzip 压缩的单日日志归档，只保留 day 当天的行。
// day 必须是当天 0 点（带时区）；文件不存在时返回的错误满足 errors.Is(err, fs.ErrNotExist)。
func ReadArchive(path string, day time.Time) ([]Line, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s 不是有效的 gzip 文件: %w", path, err)
	}
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("解压 %s 失败: %w", path, err)
	}
	if len(data) > maxArchiveBytes {
		return nil, fmt.Errorf("%s 解压后超过 %dMB 上限", path, maxArchiveBytes>>20)
	}
	// 日志行没有年份：以该日最后一秒作为"现在"来推断年份，跨年读取旧文件也能得到正确的年份
	end := day.AddDate(0, 0, 1).Add(-time.Second)
	return OnDay(Parse(string(data), end), day), nil
}

// OnDay 只保留时间落在 day 当天（day 为当天 0 点）的行；没有时间的行丢弃。
func OnDay(lines []Line, day time.Time) []Line {
	next := day.AddDate(0, 0, 1)
	var out []Line
	for _, l := range lines {
		if l.HasTime && !l.Time.Before(day) && l.Time.Before(next) {
			out = append(out, l)
		}
	}
	return out
}
