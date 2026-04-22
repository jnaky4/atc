package files

import (
	"bufio"
	"filesystem/cli/colors"
	selection "filesystem/cli/select"
	"filesystem/const/regexStr"
	tc "filesystem/const/terminalColors"
	t "filesystem/time_completion"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/chzyer/readline"
	"github.com/eiannone/keyboard"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

// directoryCache stores already-calculated directory structures to avoid recalculation
var directoryCache = make(map[string]*Directory)
var cacheMutex sync.RWMutex
var logLevel = "error"

func ParseFile(fullPath string, parent *Directory) (*FileInfo, error) {
	fi, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	file := &FileInfo{
		Name:        fi.Name(),
		Permissions: fmt.Sprintf("%o", fi.Mode().Perm()),
		Owner:       GetOwner(fi),
		Group:       GetGroup(fi),
		Size:        fi.Size(),
		ModTime:     fi.ModTime().Unix(),
		FullPath:    fullPath,
		Parent:      parent,
	}

	return file, nil
}

func ParseDirectory(path string, parent *Directory) (*Directory, error) {
	dirInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	dir := &Directory{
		FileInfo: FileInfo{
			Name:        dirInfo.Name(),
			Permissions: fmt.Sprintf("%o", dirInfo.Mode().Perm()), // Convert permissions to octal string
			Owner:       GetOwner(dirInfo),
			Group:       GetGroup(dirInfo),
			ModTime:     dirInfo.ModTime().Unix(),
			FullPath:    path,
			Parent:      parent,
		},

		Subdirectories: make(map[string]*Directory),
		Files:          make(map[string]*FileInfo),
	}

	return dir, nil
}

// BuildDirectoryStructure initializes the population of the directory structure from the dirPath
func BuildDirectoryStructure(dirPath string) (*Directory, error) {
	defer t.Timer()()
	startDir, err := ParseDirectory(dirPath, nil)
	if err != nil {
		return nil, err
	}

	//if Root == nil {
	//	return fmt.Errorf("root directory is not set")
	//}

	startDir.Size, startDir.SubObjectCount, err = BuildDirectoryRecursion(startDir)

	// Cache the built directory and its subdirectories
	cacheDirectoryTree(startDir)

	return startDir, nil
}

func BuildDirectoryRecursion(dir *Directory) (int64, int64, error) {
	var currentStat unix.Statfs_t
	err := unix.Statfs(dir.FullPath, &currentStat)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to statfs directory %s: %w", dir.FullPath, err)
	}
	dev := currentStat.Fsid.Val

	dirEntries, err := os.ReadDir(dir.FullPath)
	if err != nil {
		// Mark as unreadable instead of returning error
		dir.Unreadable = true
		if logLevel == "warn" {
			fmt.Printf("Failed to read directory: %s, error: %v\n", dir.FullPath, err)
		}
		return 0, 0, nil
	}

	var totalSize int64
	var totalCount int64

	for _, entry := range dirEntries {
		totalCount++
		fullPath := filepath.Clean(filepath.Join(dir.FullPath, entry.Name()))
		fi, err := entry.Info()

		if err != nil {
			return 0, 0, fmt.Errorf("failed to get info for %s: %w", fullPath, err)
		}

		//if symlink, treat as file with 0 size
		if fi.Mode()&os.ModeSymlink != 0 {
			//todo not windows compatible
			target, err := os.Readlink(fullPath)
			if err != nil {
				return 0, 0, fmt.Errorf("failed to read symlink %s: %w", fullPath, err)
			}
			dir.Files[entry.Name()] = &FileInfo{
				Name:        fi.Name(),
				Permissions: fmt.Sprintf("%o", fi.Mode().Perm()),
				Owner:       GetOwner(fi),
				Group:       GetGroup(fi),
				Size:        0,
				ModTime:     fi.ModTime().Unix(),
				FullPath:    fullPath,
				Target:      target,
			}
			continue
		}

		if fi.IsDir() {
			subDir, err := ParseDirectory(fullPath, dir)
			if err != nil {
				// Create unreadable dir
				subDir = &Directory{
					FileInfo: FileInfo{
						Name:        fi.Name(),
						Permissions: "",
						Owner:       "",
						Group:       "",
						Size:        0,
						ModTime:     fi.ModTime().Unix(),
						FullPath:    fullPath,
						Unreadable:  true,
					},
					Subdirectories: make(map[string]*Directory),
					Files:          make(map[string]*FileInfo),
				}
			}

			var subStat unix.Statfs_t
			err = unix.Statfs(fullPath, &subStat)
			if err != nil {
				subDir.Unreadable = true
				subDir.Size = 0
			} else if subStat.Fsid.Val[0] != dev[0] || subStat.Fsid.Val[1] != dev[1] {
				// Mount point, don't recurse, size 0
				subDir.Size = 0
			} else {
				subDirSize, subDirCount, _ := BuildDirectoryRecursion(subDir)
				totalSize += subDirSize
				totalCount += subDirCount
			}

			dir.Subdirectories[entry.Name()] = subDir
			continue
		}

		pfile, err := ParseFile(fullPath, dir)
		if err != nil {
			// Create unreadable file info
			pfile = &FileInfo{
				Name:        fi.Name(),
				Permissions: "",
				Owner:       "",
				Group:       "",
				Size:        0,
				ModTime:     0,
				FullPath:    fullPath,
				Unreadable:  true,
			}
		}
		dir.Files[entry.Name()] = pfile
		if !pfile.Unreadable {
			totalSize += fi.Size()
		}
	}

	dirInfo, err := os.Stat(dir.FullPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat directory %s: %w", dir.FullPath, err)
	}
	dir.Size = totalSize + dirInfo.Size()
	dir.SubObjectCount = totalCount
	return totalSize, totalCount, nil
}

// cacheDirectoryTree recursively caches a directory and all its subdirectories
func cacheDirectoryTree(dir *Directory) {
	cacheMutex.Lock()
	directoryCache[dir.FullPath] = dir
	cacheMutex.Unlock()
	for _, subDir := range dir.Subdirectories {
		cacheDirectoryTree(subDir)
	}
}

// preloadParentAndSiblings asynchronously loads parent directory and all its siblings
func preloadParentAndSiblings(currentDir *Directory) {
	parentPath := filepath.Dir(currentDir.FullPath)
	if parentPath == currentDir.FullPath || parentPath == "." {
		return
	}

	// Check if already cached
	cacheMutex.RLock()
	_, exists := directoryCache[parentPath]
	cacheMutex.RUnlock()
	if exists {
		return
	}

	go func() {
		parentDir, err := ParseDirectory(parentPath, nil)
		if err != nil {
			return
		}

		// Build full parent directory structure
		if _, _, err := BuildDirectoryRecursion(parentDir); err != nil {
			return
		}

		// Link current directory to parent if not already linked
		if currentDir.Parent == nil {
			parentDir.Subdirectories[currentDir.Name] = currentDir
			currentDir.Parent = parentDir
		}

		// Cache the parent and all its subdirectories
		cacheMutex.Lock()
		directoryCache[parentPath] = parentDir
		cacheMutex.Unlock()
		cacheDirectoryTree(parentDir)

		// Now preload the parent's parent and siblings
		grandParentPath := filepath.Dir(parentPath)
		if grandParentPath != parentPath && grandParentPath != "." {
			go func() {
				cacheMutex.RLock()
				_, exists := directoryCache[grandParentPath]
				cacheMutex.RUnlock()
				if !exists {
					grandParent, err := ParseDirectory(grandParentPath, nil)
					if err == nil {
						if _, _, err := BuildDirectoryRecursion(grandParent); err == nil {
							cacheMutex.Lock()
							directoryCache[grandParentPath] = grandParent
							cacheMutex.Unlock()
							cacheDirectoryTree(grandParent)
						}
					}
				}
			}()
		}
	}()
}

func GetOwner(fi os.FileInfo) string {
	if runtime.GOOS == "windows" {
		println("Not compatible")
	} else {
		if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
			usr, err := user.LookupId(fmt.Sprintf("%d", stat.Uid))
			if err == nil {
				return usr.Username
			}
		}
	}

	return "unknown"
}

func GetGroup(fi os.FileInfo) string {
	if runtime.GOOS == "windows" {
		println("Not compatible")
	} else {
		//todo not windows compatible
		if stat, ok := fi.Sys().(*syscall.Stat_t); ok {
			group, err := user.LookupGroupId(fmt.Sprintf("%d", stat.Gid))
			if err == nil {
				return group.Name
			}
		}
	}
	return "unknown"
}

func AutoSize(sizeInBytes int64) string {
	switch {
	case sizeInBytes < 1024:
		return fmt.Sprintf("%d B", sizeInBytes)
	case sizeInBytes < 1024*1024:
		return fmt.Sprintf("%.2f KB", float64(sizeInBytes)/1024)
	case sizeInBytes < 1024*1024*1024:
		return fmt.Sprintf("%.2f MB", float64(sizeInBytes)/(1024*1024))
	case sizeInBytes < 1024*1024*1024*1024:
		return fmt.Sprintf("%.2f GB", float64(sizeInBytes)/(1024*1024*1024))
	case sizeInBytes >= 1024*1024*1024*1024:
		return fmt.Sprintf("%.2f TB", float64(sizeInBytes)/(1024*1024*1024*1024))
	default:
		return fmt.Sprintf("%d B", sizeInBytes)
	}
}

func getOrderedDirectoryItems(dir *Directory) []string {
	var visibleItems []string
	var hiddenItems []string
	for name := range dir.Subdirectories {
		if strings.HasPrefix(name, ".") {
			hiddenItems = append(hiddenItems, name+"/")
		} else {
			visibleItems = append(visibleItems, name+"/")
		}
	}
	for name := range dir.Files {
		if strings.HasPrefix(name, ".") {
			hiddenItems = append(hiddenItems, name)
		} else {
			visibleItems = append(visibleItems, name)
		}
	}
	sort.Strings(visibleItems)
	sort.Strings(hiddenItems)
	return append(visibleItems, hiddenItems...)
}

func DisplayDirectoryNavigation(startDir *Directory) (*Directory, *FileInfo, error) {
	if err := keyboard.Open(); err != nil {
		return nil, nil, err
	}
	defer keyboard.Close()

	currentDir := startDir
	selected := 0
	prevSelected := 0
	viewStart := 0

	for {
		fmt.Print(tc.ClearScreen)

		if viewStart > 0 {
			fmt.Println("...")
		}

		// Display the parent directory path
		parentPath := filepath.Dir(currentDir.FullPath)
		if parentPath != currentDir.FullPath && parentPath != "." {
			fmt.Println(parentPath + "/")
		}

		// Display current directory details
		print(displayDirectoryDetails(currentDir))

		// Retrieve ordered items for current directory
		orderedSubFiles := getOrderedDirectoryItems(currentDir)
		var displayed []string
		if viewStart > 0 {
			displayed = append(displayed, "...")
		}
		start := viewStart
		end := min(viewStart+20, len(orderedSubFiles))
		for i := start; i < end; i++ {
			displayed = append(displayed, orderedSubFiles[i])
		}
		if end < len(orderedSubFiles) {
			displayed = append(displayed, "...")
		}

		// Display files and subdirectories with selection indicator
		displaySortedDirectory(currentDir, displayed, selected)

		// Preload parent and siblings in background for smooth navigation
		preloadParentAndSiblings(currentDir)

		// Capture keyboard input for navigation
		_, key, err := keyboard.GetKey()
		if err != nil {
			return nil, nil, err
		}

		switch key {
		case keyboard.KeyArrowUp:
			if selected > 0 && displayed[selected-1] != "..." {
				selected--
			} else if displayed[selected] == "..." {
				// Do nothing
			} else if viewStart > 0 {
				viewStart -= 1
			}
			if selected > len(displayed)-1 {
				selected = len(displayed) - 1
			}
		case keyboard.KeyArrowDown:
			if selected < len(displayed)-1 && displayed[selected+1] != "..." {
				selected++
			} else if displayed[selected] == "..." {
				// Do nothing
			} else if viewStart+20 < len(orderedSubFiles) {
				viewStart += 1
			}
			if selected > len(displayed)-1 {
				selected = len(displayed) - 1
			}
		case keyboard.KeyArrowRight:
			selectedItem := displayed[selected]
			if selectedItem == "..." {
				// Do nothing
			} else if subDir, isDir := currentDir.Subdirectories[strings.TrimSuffix(selectedItem, string(os.PathSeparator))]; isDir {
				currentDir = subDir
				prevSelected = selected
				selected = 0
				viewStart = 0
				// Preload the new current directory's parent and siblings
				preloadParentAndSiblings(currentDir)
			}
		case keyboard.KeyArrowLeft:
			parentPath := filepath.Dir(currentDir.FullPath)
			if parentPath != currentDir.FullPath && parentPath != "." {
				// Check if parent is already linked
				if currentDir.Parent != nil {
					// Parent exists, but check if it's cached and needs updating
					cacheMutex.RLock()
					_, exists := directoryCache[parentPath]
					cacheMutex.RUnlock()
					if !exists {
						// Parent not cached, recalculate and cache it asynchronously
						go func(parent *Directory, path string) {
							if _, _, err := BuildDirectoryRecursion(parent); err == nil {
								cacheMutex.Lock()
								directoryCache[path] = parent
								cacheMutex.Unlock()
								cacheDirectoryTree(parent)
							}
						}(currentDir.Parent, parentPath)
					}
					currentDir = currentDir.Parent
					if len(currentDir.Subdirectories)+len(currentDir.Files) > prevSelected {
						selected = prevSelected
					} else {
						selected = 0
					}
					viewStart = 0
					// Preload the new current directory's parent and siblings
					preloadParentAndSiblings(currentDir)
				} else {
					// No parent link yet, build and cache the parent
					// Check if parent is cached first
					cacheMutex.RLock()
					cachedParent, exists := directoryCache[parentPath]
					cacheMutex.RUnlock()
					if exists {
						currentDir = cachedParent
						selected = 0
						viewStart = 0
					} else {
						// Build parent directory fully for correct display
						parentDir, err := ParseDirectory(parentPath, nil)
						if err == nil {
							// Synchronously populate parent with all immediate contents
							if _, _, err := BuildDirectoryRecursion(parentDir); err == nil {
								// Set current dir as child of parent
								parentDir.Subdirectories[currentDir.Name] = currentDir
								currentDir.Parent = parentDir
								currentDir = parentDir
								selected = 0
								viewStart = 0

								// Cache the populated parent asynchronously
								go func() {
									cacheMutex.Lock()
									directoryCache[parentPath] = parentDir
									cacheMutex.Unlock()
									// Also cache all subdirectories
									cacheDirectoryTree(parentDir)
								}()
								// Preload the new current directory's parent and siblings
								preloadParentAndSiblings(currentDir)
							}
						}
					}
				}
			}
		case keyboard.KeyEnter:
			selectedItem := orderedSubFiles[selected]
			if subDir, isDir := currentDir.Subdirectories[strings.TrimSuffix(selectedItem, string(os.PathSeparator))]; isDir {
				if !subDir.Unreadable {
					return subDir, nil, nil
				}
			}
			if fileInfo, isFile := currentDir.Files[selectedItem]; isFile {
				if !fileInfo.Unreadable {
					return nil, fileInfo, nil
				}
			}
		case keyboard.KeyCtrlC:
			fmt.Println("\nTerminating...")
			keyboard.Close()
			os.Exit(0)
		default:
		}
	}
}

func displayDirectoryDetails(Dir *Directory) string {
	color := tc.ElectricBlue
	if Dir.Unreadable {
		color = tc.Gray
	}
	colorDirName := colors.SetColor(Dir.Name+string(os.PathSeparator), color)
	colorSubObjectCount := colors.SetColor(fmt.Sprint(Dir.SubObjectCount), tc.Crimson)
	colorDirOctal := colors.SetColor(Dir.Permissions, tc.Gold)
	colorDirOwner := colors.SetColor(Dir.Owner, tc.VibrantPink)
	colorDirGroup := colors.SetColor(Dir.Group, tc.Fuchsia)
	colorDirSize := colors.SetColor(AutoSize(Dir.Size), tc.SeaGreen)
	colorTarget := ""
	if Dir.Target != "" {
		colorTarget = colors.SetColor(fmt.Sprintf(" -> %s", Dir.Target), tc.Orange)
	}
	return fmt.Sprintf("%s %s %s %s:%s %s%s\n", colorDirName, colorSubObjectCount, colorDirOctal, colorDirOwner, colorDirGroup, colorDirSize, colorTarget)
}

func displayFileDetails(fileInfo *FileInfo) string {
	color := tc.Mint
	if fileInfo.Unreadable {
		color = tc.Gray
	}
	colorFileName := colors.SetColor(fileInfo.Name, color)
	colorFileOctal := colors.SetColor(fileInfo.Permissions, tc.Gold)
	colorFileOwner := colors.SetColor(fileInfo.Owner, tc.VibrantPink)
	colorFileGroup := colors.SetColor(fileInfo.Group, tc.Fuchsia)
	colorFileSize := colors.SetColor(AutoSize(fileInfo.Size), tc.SeaGreen)
	colorTarget := ""
	if fileInfo.Target != "" {
		colorTarget = colors.SetColor(fmt.Sprintf(" -> %s", fileInfo.Target), tc.Orange)
	}

	return fmt.Sprintf("%s %s %s:%s %s%s\n", colorFileName, colorFileOctal, colorFileOwner, colorFileGroup, colorFileSize, colorTarget)
}

func displaySortedDirectory(currentDir *Directory, options []string, selected int) {
	for i, option := range options {
		if i == selected {
			fmt.Print(tc.Coral + "> ")
		} else {
			fmt.Print("  ")
		}

		if option == "..." {
			fmt.Println("...")
			continue
		}

		if subDir, isDir := currentDir.Subdirectories[strings.TrimSuffix(option, string(os.PathSeparator))]; isDir {
			print(displayDirectoryDetails(subDir))
		} else if fileInfo, isFile := currentDir.Files[option]; isFile {
			print(displayFileDetails(fileInfo))
		}
	}
}

func DirectorySelect(selectedDir *Directory) string {
	option, err := selection.SelectOption(displayDirectoryDetails(selectedDir), GetDirectoryOptions())
	if err != nil {
		println(err.Error())
		return ""
	}
	return option
}

func FileSelect(selectedFile *FileInfo) string {
	option, err := selection.SelectOption(displayFileDetails(selectedFile), GetDirectoryOptions())
	if err != nil {
		println(err.Error())
		return ""
	}
	return option
}

func Confirm(message string) bool {
	options := []string{"No", "Yes"}
	selected := 0

	if err := keyboard.Open(); err != nil {
		return false
	}
	defer keyboard.Close()

	for {
		fmt.Print(tc.ClearScreen) // Clear the screen

		println(message)
		// Render options with arrows and color
		for i, option := range options {
			if i == selected {
				print(colors.SetColor(fmt.Sprintf("> %-3s <", option), tc.Coral))
			} else {
				print(colors.SetColor(fmt.Sprintf("  %-3s  ", option), tc.Default))
			}
		}
		fmt.Println()

		_, key, err := keyboard.GetKey()
		if err != nil {
			return false
		}

		switch key {
		case keyboard.KeyArrowRight:
			selected = (selected + 1) % len(options)
		case keyboard.KeyArrowLeft:
			selected = (selected - 1 + len(options)) % len(options)
		case keyboard.KeyEnter:
			return selected == 1
		case keyboard.KeyCtrlC:
			fmt.Println("\nTerminating...")
			os.Exit(0)
		default:
		}
	}
}

func Deletion(file *FileInfo, details string, isDir bool) error {
	if Confirm(fmt.Sprintf("%sAre you sure you want to delete %s?", details, file.Name)) {
		if isDir {
			err := os.RemoveAll(file.FullPath)
			if err != nil {
				return err
			}
		} else {
			err := os.Remove(file.FullPath)
			if err != nil {
				return err
			}
		}
	}

	if _, exists := file.Parent.Files[file.Name]; exists {
		delete(file.Parent.Files, file.Name)
	} else if _, exists = file.Parent.Subdirectories[file.Name]; exists {

		delete(file.Parent.Subdirectories, file.Name)
	}

	updateParentSizes(file.Parent, -1*file.Size)
	updateParentCount(file.Parent, -1)

	return nil
}

func Renaming(file *FileInfo) {
	var newName string
	var valid bool

	for newName == "" || !valid {
		newName = PlaceHolderInput(colors.SetColor("Enter a new name: ", tc.SeaGreen), file.Name)
		valid, _ = regexp.MatchString(regexStr.UnixFile, newName)
		if !valid {
			println("Invalid Input")
		}
	}

	err := os.Rename(file.FullPath, filepath.Join(filepath.Dir(file.FullPath), newName))
	if err != nil {
		println(err.Error())
	}

	if _, exists := file.Parent.Files[file.Name]; exists {
		file.Parent.Files[newName] = file.Parent.Files[file.Name]
		delete(file.Parent.Files, file.Name)
	} else if _, exists = file.Parent.Subdirectories[file.Name]; exists {
		file.Parent.Subdirectories[newName] = file.Parent.Subdirectories[file.Name]
		delete(file.Parent.Subdirectories, file.Name)
	}

	file.Name = newName

}

func SetPermissions(file *FileInfo) error {
	var permissions string
	var valid bool

	for permissions == "" || !valid {
		print(colors.SetColor("Enter Octal Permissions: ", tc.SeaGreen))
		_, err := fmt.Scanln(&permissions)
		if err != nil {
			println(err.Error())
		}

		valid, _ = regexp.MatchString(regexStr.Octal, permissions)
		if !valid {
			println("Invalid Input")
		} else {
			file.Permissions = permissions
		}
	}

	mode, err := strconv.ParseUint(permissions, 8, 32)
	if err != nil {
		return fmt.Errorf("invalid permission mode: %v", err)
	}

	err = os.Chmod(file.FullPath, os.FileMode(mode))
	if err != nil {
		return fmt.Errorf("failed to set permissions: %v", err)
	}

	return nil

}

func SetOwnership(file *FileInfo) error {
	return nil
}

func Grepping(grep string) []string {
	return []string{}
}

/* TODO
case string(Backup):
case string(Compare):
case string(Ownership):
case string(Stat):
case string(SimLink):
case string(Grep):
auto complete
multi threading
letter inputs for keyboard that filter result
pagination
edit files via scp?
*/

// MovingDir TODO update files sizes after move
func MovingDir(dir *Directory) {
}

// MovingFile TODO update files sizes after move
func MovingFile(selectedFile *FileInfo) {
}

func CopyingFolder(selectedDir *Directory) {
	var copyPath string
	var valid bool

	for copyPath == "" || !valid {
		copyPath = PlaceHolderInput(colors.SetColor("Enter a new path: ", tc.SeaGreen), fmt.Sprintf("%s_copy", selectedDir.FullPath))

		valid, _ = regexp.MatchString(regexStr.UnixAbsFilePath, copyPath)
		//valid, _ = regexp.MatchString(fmt.Sprintf(`^([a-zA-Z0-9._-]+%c)*[a-zA-Z0-9._-]+$`, os.PathSeparator), copyName)
		if !valid {
			println("Invalid Input")
		}

	}
	err := CopyFolder(selectedDir.FullPath, copyPath)
	if err != nil {
		println(err.Error())
	}
	//why not use BuildDirectoryStructure?
	//newDir := Directory{
	//	FileInfo: FileInfo{
	//		Name: filepath.Base(copyPath),
	//		Permissions: selectedDir.Permissions,
	//		Owner: selectedDir.Owner,
	//		Group: selectedDir.Group,
	//		Size: selectedDir.Size,
	//		ModTime: time.Now().,
	//	}
	//	SubObjectCount: selectedDir.SubObjectCount,
	//	Files: make(map[string]FileInfo),
	//	Subdirectories: make(map[string]*Directory),
	//
	//}

}

//todo print errors at top?

func CopyFolder(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}
		return CopyFile(path, destPath)
	})

}

// todo auto complete and Boyer-Moore, get accurate file size from metadata
func CopyingFile(selectedFile *FileInfo) {
	var copyPath string
	var valid bool
	fileSlice := strings.Split(selectedFile.FullPath, ".")

	for copyPath == "" || !valid {
		copyPath = PlaceHolderInput(colors.SetColor("Enter a new path: ", tc.SeaGreen), fmt.Sprintf("%s_copy.%s", fileSlice[0], fileSlice[1]))

		valid, _ = regexp.MatchString(regexStr.UnixAbsFilePath, copyPath)
		if !valid {
			println("Invalid Input")
		}
	}

	err := CopyFile(selectedFile.FullPath, copyPath)
	if err != nil {
		println(err.Error())
	}

	//todo merge with updateCount and updateSize
	updateFileSystem(selectedFile, copyPath)
}

func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return err
	}

	// Sync to ensure all content is flushed to disk
	err = destinationFile.Sync()
	return err
}

func PlaceHolderInput(prompt string, placeHolder string) string {
	var input string

	rl, err := readline.NewEx(&readline.Config{
		Prompt: prompt,
		//HistoryFile: "/tmp/readline.tmp", // Optional, for input history
	})
	if err != nil {
		fmt.Println(err.Error())
	}
	defer rl.Close()

	// Set default text
	_, err = rl.WriteStdin([]byte(placeHolder))
	if err != nil {
		println(err.Error())
	}

	// Read the input
	input, err = rl.Readline()
	if err != nil {
		fmt.Println(err.Error())
	}

	return input
}

func OpenWithDefault(path string) error {
	var cmd *exec.Cmd
	// Prefer VS Code if available
	if _, err := exec.LookPath("code"); err == nil {
		cmd = exec.Command("code", path)
	} else {
		// Fall back to OS default
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", path)
		case "darwin":
			cmd = exec.Command("open", path)
		default: // linux and others
			cmd = exec.Command("xdg-open", path)
		}
	}
	return cmd.Start()
}

func HandleDirOperation(operation string, dir *Directory) {
	switch operation {
	case string(Zip):
		Compressor := &GzipCompressor{}
		err := ZipFileSystemObject(dir.FullPath, dir.FullPath+".zip", Compressor)
		if err != nil {
			println(err.Error())
		}
	case string(Delete):
		err := Deletion(&dir.FileInfo, displayDirectoryDetails(dir), true)
		if err != nil {
			println(err.Error())
		}
	case string(Move):
		MovingDir(dir)
	case string(Copy):
		CopyingFolder(dir)
	case string(Backup):
	case string(Compare):
	case string(Open):
		err := OpenWithDefault(dir.FullPath)
		if err != nil {
			println(err.Error())
		}
	case string(Ownership):
	case string(Permissions):
		//todo not windows compatible
		err := SetPermissions(&dir.FileInfo)
		if err != nil {
			println(err.Error())
		}
	case string(Rename):
		Renaming(&dir.FileInfo)
	case string(Stat):
	case string(SimLink):
	case string(Grep):
	default:
	}
}

func HandleFileOperation(operation string, selectedFile *FileInfo) {
	switch operation {
	//TODO check if zipped to unzip
	case string(Zip):
		Compressor := &GzipCompressor{}
		err := ZipFileSystemObject(selectedFile.FullPath, selectedFile.FullPath+".zip", Compressor)
		if err != nil {
			println(err.Error())
		}
		updateFileSystem(selectedFile, selectedFile.FullPath+".zip")
	case string(Delete):
		err := Deletion(selectedFile, displayFileDetails(selectedFile), false)
		if err != nil {
			println(err.Error())
		}
	case string(Move):
		MovingFile(selectedFile)
	case string(Copy):
		CopyingFile(selectedFile)
	case string(Permissions):
		err := SetPermissions(selectedFile)
		if err != nil {
			println(err.Error())
		}
	case string(Open):
		err := OpenWithDefault(selectedFile.FullPath)
		if err != nil {
			println(err.Error())
		}
	case string(Rename):
		Renaming(selectedFile)
	case string(Backup):

	case string(Compare):
	case string(Ownership):
	case string(Stat):
	case string(SimLink):
	case string(Grep):
	default:
	}
}

func updateFileSystem(file *FileInfo, newPath string) {

	if !strings.HasPrefix(newPath, Root.FullPath) {
		return
	}

	//todo get fileinfo from created file

	newFile := &FileInfo{
		Name:        filepath.Base(newPath),
		Permissions: file.Permissions,
		Owner:       file.Owner,
		Group:       file.Group,
		Size:        file.Size,
		ModTime:     file.ModTime, //todo change
		FullPath:    newPath,
		Target:      file.Target,
	}

	targetDir := findTargetDirectory(filepath.Dir(newPath), Root)
	if targetDir == nil {
		return
	}

	newFile.Parent = targetDir
	targetDir.Files[newFile.Name] = newFile
	targetDir.SubObjectCount++
	updateParentSizes(targetDir, newFile.Size)

}

func findTargetDirectory(targetPath string, dir *Directory) *Directory {
	if dir.FullPath == targetPath {
		return dir
	}

	for _, subDir := range dir.Subdirectories {
		if result := findTargetDirectory(targetPath, subDir); result != nil {
			return result
		}
	}
	return nil
}

//func update() {
//	updateParentSizes(file.Parent, -1*file.Size)
//	updateParentCount(filepath.HasPre)
//}

func updateParentCount(dir *Directory, fileCount int64) {
	for currentDir := dir; currentDir != nil; currentDir = currentDir.Parent {
		currentDir.SubObjectCount += fileCount
	}
}

func updateParentSizes(dir *Directory, fileSize int64) {
	for currentDir := dir; currentDir != nil; currentDir = currentDir.Parent {
		currentDir.Size += fileSize
	}
}

//todo remove old code

func grep(pattern, content string) []string {
	var matches []string
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, pattern) {
			matches = append(matches, fmt.Sprintf("Line %d: %s", i+1, line))
		}
	}
	return matches
}

func PrintDirStructure(dir *Directory) {
	printDirectory(dir, 0)
}

func printDirectory(dir *Directory, level int) {
	// Determine the level of indentation
	indent := strings.Repeat("  ", level)

	// Print directory details
	colorDirName := colors.SetColor(dir.Name+string(os.PathSeparator), tc.Blue)
	colorDirOctal := colors.SetColor(dir.Permissions, tc.Gold)
	colorDOwner := colors.SetColor(dir.Owner, tc.Crimson)
	colorDGroup := colors.SetColor(dir.Group, tc.Fuchsia)
	colorDSize := colors.SetColor(AutoSize(dir.Size), tc.SeaGreen)

	fmt.Printf("%s%s %s %s:%s %s\n", indent, colorDirName, colorDirOctal, colorDOwner, colorDGroup, colorDSize)

	//// Iterate through links and print them
	//for linkName, linkInfo := range dir.Links {
	//	fmt.Printf("%s  L:%s/%s %s %s:%s %s\n", indent, dir.Name, linkName, linkInfo.Permissions, linkInfo.Owner, linkInfo.Group, linkInfo.Size)
	//}

	// Iterate through files and print the

	for fileName, fileInfo := range dir.Files {
		colorFileName := colors.SetColor(fileName, tc.Mint)
		colorFileOctal := colors.SetColor(fileInfo.Permissions, tc.Gold)
		colorOwner := colors.SetColor(fileInfo.Owner, tc.Crimson)
		colorGroup := colors.SetColor(fileInfo.Group, tc.Fuchsia)
		colorSize := colors.SetColor(AutoSize(fileInfo.Size), tc.SeaGreen)
		fmt.Printf("%s  %s %s %s:%s %s\n", indent, colorFileName, colorFileOctal, colorOwner, colorGroup, colorSize)
	}

	// Recurse into subdirectories
	for _, subDir := range dir.Subdirectories {
		printDirectory(subDir, level+1)
	}
}

func WalkDirectory(directory, pattern string) error {
	return filepath.WalkDir(directory, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return processFile(path, pattern)
		}

		return nil
	})
}

func ListExecutableFileDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filename)
}

func GetSize(path string) (int64, error) {
	var totalSize int64

	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			fileInfo, err := d.Info()
			if err != nil {
				return err
			}
			totalSize += fileInfo.Size()
		}
		return nil
	})

	if err != nil {
		return 0, err
	}

	return totalSize, nil
}

// processFile reads the content of a file and searches for the pattern using grep.
func processFile(filePath, pattern string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var content strings.Builder
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		content.WriteString(scanner.Text() + "\n")
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	matches := grep(pattern, content.String())
	if len(matches) > 0 {
		fmt.Printf("File: %s\n", filePath)
		for _, match := range matches {
			fmt.Println(match)
		}
	}

	return nil
}

func YamlToDict(fPath string) map[interface{}]interface{} {
	obj := make(map[interface{}]interface{})
	yamlFile, err := os.ReadFile(fPath)
	if err != nil {
		fmt.Printf("yamlFile.Get err #%v ", err)
	}
	err = yaml.Unmarshal(yamlFile, obj)
	if err != nil {
		fmt.Printf("Unmarshal: %v", err)
	}

	return obj
}
