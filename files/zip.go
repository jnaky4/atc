package files

import (
	"archive/zip"
	"compress/gzip"
	"github.com/andybalholm/brotli"
	"github.com/golang/snappy"
	"github.com/klauspost/compress/zstd"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//Missing:
// LZ4: Extremely fast compression and decompression speeds, making it ideal for real-time data processing and applications where performance is critical, like logging.
// Bzip2: Achieves high compression ratios, making it excellent for large files and archives where saving disk space is a priority, despite being slower than others.

type CompressionStrategy interface {
	Compress(w io.Writer, r io.Reader) error
}

// Snappy: Prioritizes speed over compression ratio. Best for scenarios where quick access to data is essential, such as in databases or caching.
type SnappyCompressor struct{}

func (s *SnappyCompressor) Compress(w io.Writer, r io.Reader) error {
	writer := snappy.NewBufferedWriter(w)
	defer writer.Close()
	_, err := io.Copy(writer, r)
	return err
}

// Gzip: Fast and widely supported, ideal for compressing text files like HTML, CSS, and JavaScript. Great for reducing file size in web applications.
type GzipCompressor struct{}

func (g *GzipCompressor) Compress(w io.Writer, r io.Reader) error {
	writer := gzip.NewWriter(w)
	defer writer.Close()
	_, err := io.Copy(writer, r)
	return err
}

// Brotli: Offers better compression rates than Gzip, especially for web content. Best for serving compressed web assets to improve load times.
type BrotliCompressor struct{}

func (b *BrotliCompressor) Compress(w io.Writer, r io.Reader) error {
	brotliWriter := brotli.NewWriter(w)
	defer brotliWriter.Close()
	_, err := io.Copy(brotliWriter, r)
	return err
}

// Zstd: Balances speed and compression ratio well. Suitable for large datasets in backups where you want a good compression without sacrificing too much speed.
type ZstdCompressor struct{}

func (z *ZstdCompressor) Compress(w io.Writer, r io.Reader) error {
	zstdWriter, err := zstd.NewWriter(w)
	if err != nil {
		return err
	}
	defer zstdWriter.Close()
	_, err = io.Copy(zstdWriter, r)
	return err
}

// ZipFileSystemObject compresses the specified directory using the provided compression strategy
func ZipFileSystemObject(sourceDir, zipFilePath string, compressor CompressionStrategy) error {
	zipFile, err := os.Create(zipFilePath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Preserve the directory structure
		header.Name = strings.TrimPrefix(path, filepath.Dir(sourceDir)+"/")
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate // Use zip.Deflate as the default method
		}

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		// If it's a file, copy the contents
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			// Use the provided compression strategy
			if err := compressor.Compress(writer, file); err != nil {
				return err
			}
		}

		return nil
	})

	return err
}
