package regexStr

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

//https://regex101.com/

func TestFilePaths(t *testing.T) {
	tests := []struct {
		platform string
		fullPath string
		valid    bool
	}{
		// Valid Windows paths
		{"Windows", "C:\\Path_1-\\Path2\\Path.txt", true},
		{"Windows", "C:\\Users\\user\\Documents\\file.txt", true},
		{"Windows", "D:\\Program Files\\app\\app.exe", true},
		{"Windows", "C:\\Windows\\System32\\config.sys", true},
		{"Windows", "E:\\Backup\\photos\\image.jpg", true},
		{"Windows", "C:\\Users\\user\\Desktop\\file_with_underscore_and-dash.txt", true},
		{"Windows", "F:\\external_drive\\music\\song.mp3", true},
		{"Windows", "C:\\Program Files (x86)\\Microsoft Office\\word.exe", true},
		{"Windows", "D:\\Downloads\\installer.exe", true},
		{"Windows", "C:\\Users\\JaneDoe\\Documents\\MyFiles\\report.pdf", true},
		// Invalid Windows paths
		{"Windows", "C:\\Users\\user\\Documents\\", false},
		{"Windows", "D:\\Program Files\\app\\app", false},
		{"Windows", "C:\\Windows\\System32\\config", false},
		{"Windows", "E:\\Backup\\photos\\image", false},
		{"Windows", "C:\\Program Files (x86)\\Microsoft Office\\", false},
		{"Windows", "C:\\Windows\\System32\\invali?d\\path.txt", false},
		{"Windows", "C:\\Users\\user\\Desktop\\fi|le.txt", false},
		{"Windows", "C:\\temp\\*file.txt", false},
		{"Windows", "F:\\external_drive/music/some<song.mp3", false},
		{"Windows", "C:\\Program|Files\\app\\app.exe", false},

		// Valid Unix paths
		{"Unix", "/home/user/file.txt", true},
		{"Unix", "/opt/program/README.md", true},
		{"Unix", "/usr/local/bin/script.sh", true},
		{"Unix", "/etc/nginx/nginx.conf", true},
		{"Unix", "/root/.bashrc", true},
		{"Unix", "/tmp/temporaryfile", true},
		{"Unix", "/opt/program/README.md", true},
		{"Unix", "/Users/johndoe/Documents/file.pdf", true},
		{"Unix", "/home/user/projects/file_with_underscore_and-dash.txt", true},
		{"Unix", "/media/external_drive/music/song.mp3", true},

		// Invalid Unix paths
		{"Unix", "/home/user/", false},
		{"Unix", "/etc/nginx//nginx.conf", false},
		{"Unix", "/var/log//logfile.log", false},
		{"Unix", "/root/.bashrc/", false},
		{"Unix", "/tmp/", false},
		{"Unix", "/Users/johndoe//file.txt", false},
		{"Unix", "/home/user//file.txt", false},
		{"Unix", "/media/external_drive/music/ song.mp3", false},
	}

	for _, tt := range tests {

		t.Run("Test_"+tt.fullPath, func(t *testing.T) {
			var rootRegex, pathRegex, fileRegex, fullPathRegex string
			if tt.platform == "Windows" {
				rootRegex = WindowsRoot
				pathRegex = WindowsDirectory
				fileRegex = WindowsFile
				fullPathRegex = WindowsABSFilePath
			} else if tt.platform == "Unix" {
				rootRegex = UnixRoot
				pathRegex = UnixDirectory
				fileRegex = UnixFile
				fullPathRegex = UnixAbsFilePath
			}

			var root, path, file string
			if tt.platform == "Unix" {
				root, path, file = extractUnixParts(tt.fullPath)
			} else {
				root, path, file = extractWindowsParts(tt.fullPath)
			}

			matchedRoot, _ := regexp.MatchString(rootRegex, root)
			matchedPath, _ := regexp.MatchString(pathRegex, path)
			matchedFile, _ := regexp.MatchString(fileRegex, file)
			matchedFullPath, _ := regexp.MatchString(fullPathRegex, tt.fullPath)

			if tt.valid {
				if matchedRoot != tt.valid || matchedPath != tt.valid || matchedFile != tt.valid || matchedFullPath != tt.valid {
					t.Errorf("Valid Regex failed: \nroot->%s->%t \npath->%s->%t \nfile->%s->%t \nfullpath->%s->%t ", root, matchedRoot, path, matchedPath, file, matchedFile, tt.fullPath, matchedFullPath)
				}
			} else if matchedRoot != tt.valid && matchedPath != tt.valid && matchedFile != tt.valid && matchedFullPath != tt.valid {
				t.Errorf("Invalid Regex failed")
			}
		})
	}
}

func extractWindowsParts(fullPath string) (string, string, string) {
	root := fullPath[:2]
	rest := fullPath[2:]
	parts := strings.LastIndex(rest, "\\")

	if parts == -1 {
		return root, "", rest
	}

	file := rest[parts+1:]
	path := rest[:parts+1]

	return root, path, file
}

func extractUnixParts(fullPath string) (string, string, string) {
	root := string(fullPath[0])
	file := filepath.Base(fullPath)
	path := filepath.Dir(fullPath)

	return root, path, file
}
