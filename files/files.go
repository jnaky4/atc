package files

import (
	"bufio"
	"bytes"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/eiannone/keyboard"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// directoryCache stores already-calculated directory structures to avoid recalculation
var directoryCache = make(map[string]*Directory)
var cacheMutex sync.RWMutex
var logLevel string

// displayDirty is set to 1 by finalizeDirectory whenever a directory size is
// written.  The display loop checks this flag on a 250 ms ticker and redraws
// only when something has changed, capping background-driven repaints at 4 per
// second so the screen stays readable while a large tree is scanning.
var displayDirty int32

// numWorkers and scanSem are set by init() after Cfg is loaded so the pool
// size can be driven by config (workers=0 means auto: runtime.NumCPU()*2).
var numWorkers int
var scanSem chan struct{}

func init() {
	Cfg = loadOrCreateConfig()

	logLevel = Cfg.Logging.Level

	numWorkers = runtime.NumCPU() * 2
	scanSem = make(chan struct{}, numWorkers)
}

// screenBufPool reuses *bytes.Buffer instances across screen redraws.
// A single pooled buffer assembles the entire frame before writing,
// replacing O(n) per-item allocations and write syscalls with one each.
var screenBufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 16384))
	},
}

// dirSlicePool reuses the needsScan []*Directory slice inside scanDirectory.
// With thousands of directories scanned during startup, this eliminates
// thousands of small slice allocations that would pressure the GC.
var dirSlicePool = sync.Pool{
	New: func() any {
		s := make([]*Directory, 0, 32)
		return &s
	},
}

// scanDirectory scans exactly one directory level: populates dir.Files,
// dir.Subdirectories, and dir.SubObjectCount, then spawns bounded goroutines
// for each subdirectory that lives on the same filesystem.
//
// The caller must have already acquired one slot from scanSem.
// scanDirectory releases that slot before it returns so that child goroutines
// can acquire their own slots without deadlocking.
//
// When the last child finishes it calls finalizeDirectory, which rolls sizes
// up the tree bottom-to-top.  BuildDirectoryStructure returns as soon as the
// first level is populated; all deeper work happens in the background.
func scanDirectory(dir *Directory, fsidDev [2]int32) {
	entries, err := os.ReadDir(dir.FullPath)
	if err != nil {
		dir.Unreadable = true
		<-scanSem
		finalizeDirectory(dir)
		return
	}

	needsScanPtr := dirSlicePool.Get().(*[]*Directory)
	needsScan := (*needsScanPtr)[:0]
	defer func() {
		*needsScanPtr = needsScan[:0]
		dirSlicePool.Put(needsScanPtr)
	}()

	for _, entry := range entries {
		fullPath := filepath.Clean(filepath.Join(dir.FullPath, entry.Name()))
		fi, err := entry.Info()
		if err != nil {
			continue
		}

		// Symlink – record target, no recursion
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(fullPath)
			dir.mu.Lock()
			dir.Files[entry.Name()] = &FileInfo{
				Name:        fi.Name(),
				Permissions: fmt.Sprintf("%o", fi.Mode().Perm()),
				Owner:       GetOwner(fi),
				Group:       GetGroup(fi),
				Size:        0,
				ModTime:     fi.ModTime().Unix(),
				FullPath:    fullPath,
				Target:      target,
				Parent:      dir,
			}
			dir.mu.Unlock()
			continue
		}

		if fi.IsDir() {
			subDir, err := ParseDirectory(fullPath, dir)
			if err != nil {
				if logLevel == "warn" {
					fmt.Printf("ParseDirectory failed for %s: %v\n", fullPath, err)
				}
				continue
			}

			dir.mu.Lock()
			dir.Subdirectories[entry.Name()] = subDir
			dir.mu.Unlock()

			// Stay on the same filesystem – skip mount points
			var subStat unix.Statfs_t
			if err := unix.Statfs(fullPath, &subStat); err != nil {
				subDir.Unreadable = true
				// Treat as a leaf with size 0; finalizeDirectory handles it
				continue
			}
			if subStat.Fsid.Val[0] != fsidDev[0] || subStat.Fsid.Val[1] != fsidDev[1] {
				// Mount point: leave Size=0, do not recurse
				continue
			}

			needsScan = append(needsScan, subDir)
			continue
		}

		// Regular file
		pfile, err := ParseFile(fullPath, dir)
		if err != nil {
			pfile = &FileInfo{
				Name:       fi.Name(),
				FullPath:   fullPath,
				Unreadable: true,
				Parent:     dir,
			}
		}
		dir.mu.Lock()
		dir.Files[entry.Name()] = pfile
		dir.mu.Unlock()
	}

	dir.mu.Lock()
	dir.SubObjectCount = int64(len(entries))
	dir.mu.Unlock()

	// Release our semaphore slot BEFORE trying to acquire slots for children.
	// If we held it while blocking on child slots we would deadlock when the
	// pool is saturated.
	<-scanSem

	if len(needsScan) == 0 {
		finalizeDirectory(dir)
		return
	}

	// pendingChildren must be stored before any child goroutine starts so
	// that a fast-finishing child never decrements below the final count.
	atomic.StoreInt32(&dir.pendingChildren, int32(len(needsScan)))

	for _, sd := range needsScan {
		sd := sd
		go func() {
			scanSem <- struct{}{} // acquire slot (blocks if pool is saturated)
			scanDirectory(sd, fsidDev)
		}()
	}
}

// finalizeDirectory is called exactly once per directory, after every
// descendant has been scanned.  It computes the recursive size from the
// completed subtree, writes it under the directory's own lock, and
// decrements the parent's pendingChildren counter.  When the counter
// reaches zero the parent finalizes itself, and so on up to the root.
func finalizeDirectory(dir *Directory) {
	dirInfo, err := os.Stat(dir.FullPath)
	var total int64
	if err == nil {
		total = dirInfo.Size()
	}

	dir.mu.RLock()
	for _, f := range dir.Files {
		if !f.Unreadable {
			total += f.Size
		}
	}
	for _, sd := range dir.Subdirectories {
		// Each sd.Size was written by its own finalizeDirectory call, which
		// happened-before this point (guaranteed by the pendingChildren atomic).
		sd.mu.RLock()
		total += sd.Size
		sd.mu.RUnlock()
	}
	dir.mu.RUnlock()

	dir.mu.Lock()
	dir.Size = total
	dir.mu.Unlock()

	atomic.StoreInt32(&dir.scanComplete, 1)

	// Mark the display as dirty so the ticker-driven redraw picks it up.
	// A simple store is sufficient; losing a concurrent store from another
	// goroutine is harmless because the flag stays 1 either way.
	atomic.StoreInt32(&displayDirty, 1)

	// Notify parent under dir's lock so that preloadParentAndSiblings can
	// atomically set Parent and read scanComplete without missing an update.
	dir.mu.Lock()
	parent := dir.FileInfo.Parent
	dir.mu.Unlock()

	if parent != nil {
		if atomic.AddInt32(&parent.pendingChildren, -1) == 0 {
			finalizeDirectory(parent)
		}
	}
}

func getTerminalWidth() int {
	width, _, err := term.GetSize(0)
	if err != nil || width < 20 {
		return 80 // safe fallback
	}
	return width
}

// getTerminalPageSize returns the number of directory entries that fit in the
// current terminal window.  It reads the live terminal height on every call so
// the listing automatically expands or contracts when the user resizes the
// window — no signal handler required.
//
// Fixed overhead per frame:
//   - 1 line  parent directory path
//   - 1 line  current directory details
//   - 2 lines top / bottom "..." scroll indicators (worst case both present)
//
// Cfg.Display.PageSize acts as a floor: the listing never shrinks below the
// configured minimum regardless of how small the terminal becomes.
func getTerminalPageSize() int {
	const overhead = 4
	_, height, err := term.GetSize(0)
	if err != nil || height <= overhead {
		return Cfg.Display.PageSize
	}
	available := height - overhead
	if available < Cfg.Display.PageSize {
		return Cfg.Display.PageSize
	}
	return available
}

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

// BuildDirectoryStructure scans the first level of dirPath synchronously so
// the caller has an immediately-usable directory listing, then returns.
// All deeper subdirectories are scanned in background goroutines.  Sizes
// start at zero and are updated in-place as subtrees complete; the display
// loop will show correct values on the next redraw after each subtree finishes.
func BuildDirectoryStructure(dirPath string) (*Directory, error) {
	defer t.Timer()()

	fmt.Printf("scan pool: %d workers (%d logical CPUs × 2)\n", numWorkers, runtime.NumCPU())

	startDir, err := ParseDirectory(dirPath, nil)
	if err != nil {
		return nil, err
	}

	if err := BuildDirectoryRecursion(startDir); err != nil {
		return nil, err
	}

	cacheDirectoryTree(startDir)
	return startDir, nil
}

// BuildDirectoryRecursion kicks off the two-phase scan+finalize process for
// dir.  It acquires one semaphore slot, calls scanDirectory (which releases
// the slot and spawns children before returning), and returns as soon as the
// first level is populated.  Sizes propagate up automatically via
// finalizeDirectory once each subtree is complete.
func BuildDirectoryRecursion(dir *Directory) error {
	var currentStat unix.Statfs_t
	if err := unix.Statfs(dir.FullPath, &currentStat); err != nil {
		return fmt.Errorf("failed to statfs directory %s: %w", dir.FullPath, err)
	}

	scanSem <- struct{}{}
	scanDirectory(dir, currentStat.Fsid.Val)
	return nil
}

// cacheDirectoryTree recursively caches a directory and all its subdirectories
func cacheDirectoryTree(dir *Directory) {
	cacheMutex.Lock()
	directoryCache[dir.FullPath] = dir
	cacheMutex.Unlock()

	// Read subdirectories under lock
	dir.mu.RLock()
	subdirs := dir.Subdirectories
	dir.mu.RUnlock()

	for _, subDir := range subdirs {
		cacheDirectoryTree(subDir)
	}
}

// preloadParentAndSiblings asynchronously loads the parent directory so that
// navigating upward feels instant.  It reuses the existing currentDir object
// (already scanned) rather than re-scanning it, and correctly wires it into
// the parent's pendingChildren accounting so that the parent's size finalizes
// as soon as all sibling scans complete.
func preloadParentAndSiblings(currentDir *Directory) {
	parentPath := filepath.Dir(currentDir.FullPath)
	if parentPath == currentDir.FullPath || parentPath == "." {
		return
	}

	cacheMutex.RLock()
	_, exists := directoryCache[parentPath]
	cacheMutex.RUnlock()
	if exists {
		return
	}

	go func() {
		var parentStat unix.Statfs_t
		if err := unix.Statfs(parentPath, &parentStat); err != nil {
			return
		}
		fsidDev := parentStat.Fsid.Val

		parentDir, err := ParseDirectory(parentPath, nil)
		if err != nil {
			return
		}

		// Scan the parent's first level manually so we can intercept the
		// entry that corresponds to currentDir and reuse the existing object.
		entries, err := os.ReadDir(parentPath)
		if err != nil {
			parentDir.Unreadable = true
			return
		}

		var needsScan []*Directory // siblings that require a fresh scan

		for _, entry := range entries {
			fullPath := filepath.Clean(filepath.Join(parentPath, entry.Name()))
			fi, err := entry.Info()
			if err != nil {
				continue
			}

			if fi.Mode()&os.ModeSymlink != 0 {
				target, _ := os.Readlink(fullPath)
				parentDir.mu.Lock()
				parentDir.Files[entry.Name()] = &FileInfo{
					Name:        fi.Name(),
					Permissions: fmt.Sprintf("%o", fi.Mode().Perm()),
					Owner:       GetOwner(fi),
					Group:       GetGroup(fi),
					ModTime:     fi.ModTime().Unix(),
					FullPath:    fullPath,
					Target:      target,
					Parent:      parentDir,
				}
				parentDir.mu.Unlock()
				continue
			}

			if fi.IsDir() {
				if fullPath == currentDir.FullPath {
					// Reuse the already-scanned directory; do not scan again.
					parentDir.mu.Lock()
					parentDir.Subdirectories[entry.Name()] = currentDir
					parentDir.mu.Unlock()
					continue
				}

				sd, err := ParseDirectory(fullPath, parentDir)
				if err != nil {
					continue
				}
				parentDir.mu.Lock()
				parentDir.Subdirectories[entry.Name()] = sd
				parentDir.mu.Unlock()

				var subStat unix.Statfs_t
				if err := unix.Statfs(fullPath, &subStat); err != nil {
					sd.Unreadable = true
					continue
				}
				if subStat.Fsid.Val[0] != fsidDev[0] || subStat.Fsid.Val[1] != fsidDev[1] {
					continue // mount point, leave Size=0
				}
				needsScan = append(needsScan, sd)
				continue
			}

			pfile, err := ParseFile(fullPath, parentDir)
			if err != nil {
				pfile = &FileInfo{Name: fi.Name(), FullPath: fullPath, Unreadable: true, Parent: parentDir}
			}
			parentDir.mu.Lock()
			parentDir.Files[entry.Name()] = pfile
			parentDir.mu.Unlock()
		}

		parentDir.mu.Lock()
		parentDir.SubObjectCount = int64(len(entries))
		parentDir.mu.Unlock()

		// pendingChildren counts siblings that need scanning, plus one slot
		// reserved for currentDir (handled below).  We set this BEFORE writing
		// currentDir.Parent so that if currentDir's finalizeDirectory call races
		// with us it will always see a valid counter.
		pendingCount := int32(len(needsScan)) + 1 // +1 for currentDir
		atomic.StoreInt32(&parentDir.pendingChildren, pendingCount)

		// Atomically link currentDir to parentDir and check whether its scan
		// has already completed.  The lock on currentDir.mu is the same one
		// that finalizeDirectory holds when it reads Parent, so one of two
		// outcomes is guaranteed:
		//   a) We write Parent first → finalizeDirectory will decrement parentDir.pendingChildren.
		//   b) finalizeDirectory ran first (scanComplete==1) → we decrement manually below.
		currentDir.mu.Lock()
		currentDir.FileInfo.Parent = parentDir
		alreadyDone := atomic.LoadInt32(&currentDir.scanComplete) == 1
		currentDir.mu.Unlock()

		if alreadyDone {
			// finalizeDirectory already ran with Parent==nil; manually consume its slot.
			if atomic.AddInt32(&parentDir.pendingChildren, -1) == 0 {
				finalizeDirectory(parentDir)
			}
		}
		// If not done, currentDir's own finalizeDirectory will decrement when it finishes.

		// Spawn bounded goroutines for siblings.
		for _, sd := range needsScan {
			sd := sd
			go func() {
				scanSem <- struct{}{}
				scanDirectory(sd, fsidDev)
			}()
		}

		cacheMutex.Lock()
		directoryCache[parentPath] = parentDir
		cacheMutex.Unlock()
		cacheDirectoryTree(parentDir)

		// Kick off grandparent preload
		grandParentPath := filepath.Dir(parentPath)
		if grandParentPath != parentPath && grandParentPath != "." {
			cacheMutex.RLock()
			_, exists := directoryCache[grandParentPath]
			cacheMutex.RUnlock()
			if !exists {
				grandParent, err := ParseDirectory(grandParentPath, nil)
				if err == nil {
					if err := BuildDirectoryRecursion(grandParent); err == nil {
						cacheMutex.Lock()
						directoryCache[grandParentPath] = grandParent
						cacheMutex.Unlock()
						cacheDirectoryTree(grandParent)
					}
				}
			}
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

func getOrderedDirectoryItems(dir *Directory, out []string) []string {
	out = out[:0]
	dir.mu.RLock()
	// Visible items first (no leading dot)
	for name := range dir.Subdirectories {
		if !strings.HasPrefix(name, ".") {
			out = append(out, name+"/")
		}
	}
	for name := range dir.Files {
		if !strings.HasPrefix(name, ".") {
			out = append(out, name)
		}
	}
	pivot := len(out) // end of visible, start of hidden

	// Hidden items are only appended when show_hidden is enabled in config.
	if Cfg.Display.ShowHidden {
		for name := range dir.Subdirectories {
			if strings.HasPrefix(name, ".") {
				out = append(out, name+"/")
			}
		}
		for name := range dir.Files {
			if strings.HasPrefix(name, ".") {
				out = append(out, name)
			}
		}
	}
	dir.mu.RUnlock()
	sort.Strings(out[:pivot])
	sort.Strings(out[pivot:])
	return out
}

func DisplayDirectoryNavigation(startDir *Directory) (*Directory, *FileInfo, error) {
	// GetKeys opens the keyboard and returns an event channel so we can also
	// select on the refresh ticker without blocking on a keypress.
	keyCh, err := keyboard.GetKeys(10)
	if err != nil {
		return nil, nil, err
	}
	defer keyboard.Close()

	// Ticker drives background size updates.  250 ms caps repaints at 4 per
	// second while a tree is scanning — smooth and readable, never disruptive.
	ticker := time.NewTicker(time.Duration(Cfg.Performance.RefreshIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	currentDir := startDir
	selected := 0
	prevSelected := 0
	viewStart := 0
	needsRedraw := true

	// orderedSubFiles and displayed are declared at loop scope so both the
	// draw path and all key handlers can reference the same snapshot.
	var orderedSubFiles []string
	var displayed []string
	// pageSize is computed from the live terminal height on every redraw and
	// stored at loop scope so key handlers always use the same value as the
	// most recent frame.
	pageSize := getTerminalPageSize()
	// lastW/lastH track the terminal dimensions as of the last draw.
	// The ticker compares against these to detect a resize without using
	// SIGWINCH, which can interfere with the keyboard package's terminal reads.
	lastW, lastH, _ := term.GetSize(0)
	// totalItems tracks the last known length of orderedSubFiles so we can
	// adjust viewStart immediately when the terminal grows large enough to
	// show everything without scrolling.
	totalItems := 0

	for {
		if needsRedraw {
			pageSize = getTerminalPageSize()
			width := getTerminalWidth()

			// If the terminal grew large enough to show all items without
			// scrolling, reset viewStart so the scroll indicators disappear.
			if totalItems > 0 && pageSize >= totalItems {
				viewStart = 0
			}
			buf := screenBufPool.Get().(*bytes.Buffer)
			buf.Reset()

			buf.WriteString(string(tc.ClearScreen))
			if viewStart > 0 {
				buf.WriteString("...\n")
			}

			parentPath := filepath.Dir(currentDir.FullPath)
			if parentPath != currentDir.FullPath && parentPath != "." {
				buf.WriteString(parentPath + "/\n")
			}

			writeDirectoryDetails(buf, currentDir, width)

			orderedSubFiles = getOrderedDirectoryItems(currentDir, orderedSubFiles)
			totalItems = len(orderedSubFiles)
			displayed = displayed[:0]
			if viewStart > 0 {
				displayed = append(displayed, "...")
			}
			start := viewStart
			end := min(viewStart+pageSize, len(orderedSubFiles))
			for i := start; i < end; i++ {
				displayed = append(displayed, orderedSubFiles[i])
			}
			if end < len(orderedSubFiles) {
				displayed = append(displayed, "...")
			}

			if len(displayed) > 0 && selected >= len(displayed) {
				selected = len(displayed) - 1
			}

			writeSortedDirectory(buf, currentDir, displayed, selected, width)

			preloadParentAndSiblings(currentDir)

			os.Stdout.Write(buf.Bytes())
			screenBufPool.Put(buf)

			needsRedraw = false
		}

		// Wait for a keypress, a ticker-driven background refresh, or a
		// terminal resize signal.
		var key keyboard.Key
		select {
		case event, ok := <-keyCh:
			if !ok {
				return nil, nil, fmt.Errorf("keyboard channel closed unexpectedly")
			}
			if event.Err != nil {
				return nil, nil, event.Err
			}
			key = event.Key
			needsRedraw = true
		case <-ticker.C:
			// Check for a terminal resize by comparing current dimensions to the
			// last known ones.  This avoids SIGWINCH entirely — the signal can
			// interrupt the keyboard package's blocking read and crash the program.
			// 250 ms lag on resize is imperceptible to the user.
			w, h, err := term.GetSize(0)
			if err == nil && (w != lastW || h != lastH) {
				lastW, lastH = w, h
				needsRedraw = true
			}
			// Also repaint if finalizeDirectory has written new sizes.
			if atomic.CompareAndSwapInt32(&displayDirty, 1, 0) {
				needsRedraw = true
			}
			continue
		}

		// Nothing to navigate if the listing is empty.
		if len(displayed) == 0 {
			continue
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
			} else if viewStart+pageSize < len(orderedSubFiles) {
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
							if err := BuildDirectoryRecursion(parent); err == nil {
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
							if err := BuildDirectoryRecursion(parentDir); err == nil {
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

// writeDirectoryDetails writes Dir's formatted details line into buf.
// Used by the hot-path draw loop to avoid allocating a string per item.
func writeDirectoryDetails(buf *bytes.Buffer, Dir *Directory, width int) {
	color := tc.ElectricBlue
	if Dir.Unreadable {
		color = tc.Gray
	}

	// Read values under lock first so we can measure their rendered widths
	// before deciding how many characters the name column can use.
	Dir.mu.RLock()
	subCount := Dir.SubObjectCount
	dirSize := Dir.Size
	Dir.mu.RUnlock()

	countStr := fmt.Sprint(subCount)
	sizeStr := AutoSize(dirSize)

	// Visible metadata width (no ANSI codes):
	//   "  "       2  — selection prefix added by writeSortedDirectory
	//   name       ?  — computed below
	//   " " count  1 + len(countStr)
	//   " " perms  1 + len(Dir.Permissions)
	//   " " owner  1 + len(Dir.Owner)
	//   ":" group  1 + len(Dir.Group)
	//   " " size   1 + len(sizeStr)
	metaWidth := 2 + 1 + len(countStr) + 1 + len(Dir.Permissions) + 1 + len(Dir.Owner) + 1 + len(Dir.Group) + 1 + len(sizeStr)
	maxNameLen := width - metaWidth
	if maxNameLen < 4 {
		maxNameLen = 4
	}

	plainName := Dir.Name + string(os.PathSeparator)
	if len(plainName) > maxNameLen {
		if maxNameLen > 3 {
			plainName = plainName[:maxNameLen-3] + "..."
		} else {
			plainName = "..."
		}
	}

	fmt.Fprintf(buf, "%s %s %s %s:%s %s",
		colors.SetColor(plainName, color),
		colors.SetColor(countStr, tc.Crimson),
		colors.SetColor(Dir.Permissions, tc.Gold),
		colors.SetColor(Dir.Owner, tc.VibrantPink),
		colors.SetColor(Dir.Group, tc.Fuchsia),
		colors.SetColor(sizeStr, tc.SeaGreen),
	)
	if Dir.Target != "" {
		fmt.Fprintf(buf, " -> %s", colors.SetColor(Dir.Target, tc.Orange))
	}
	buf.WriteByte('\n')
}

// writeFileDetails writes fileInfo's formatted details line into buf.
func writeFileDetails(buf *bytes.Buffer, fileInfo *FileInfo, width int) {
	color := tc.Mint
	if fileInfo.Unreadable {
		color = tc.Gray
	}

	sizeStr := AutoSize(fileInfo.Size)

	// Visible metadata width (no ANSI codes):
	//   "  "       2  — selection prefix
	//   name       ?  — computed below
	//   " " perms  1 + len(fileInfo.Permissions)
	//   " " owner  1 + len(fileInfo.Owner)
	//   ":" group  1 + len(fileInfo.Group)
	//   " " size   1 + len(sizeStr)
	metaWidth := 2 + 1 + len(fileInfo.Permissions) + 1 + len(fileInfo.Owner) + 1 + len(fileInfo.Group) + 1 + len(sizeStr)
	maxNameLen := width - metaWidth
	if maxNameLen < 4 {
		maxNameLen = 4
	}

	plainName := fileInfo.Name
	if len(plainName) > maxNameLen {
		if maxNameLen > 3 {
			plainName = plainName[:maxNameLen-3] + "..."
		} else {
			plainName = "..."
		}
	}

	fmt.Fprintf(buf, "%s %s %s:%s %s",
		colors.SetColor(plainName, color),
		colors.SetColor(fileInfo.Permissions, tc.Gold),
		colors.SetColor(fileInfo.Owner, tc.VibrantPink),
		colors.SetColor(fileInfo.Group, tc.Fuchsia),
		colors.SetColor(sizeStr, tc.SeaGreen),
	)
	if fileInfo.Target != "" {
		fmt.Fprintf(buf, " -> %s", colors.SetColor(fileInfo.Target, tc.Orange))
	}
	buf.WriteByte('\n')
}

func displayDirectoryDetails(Dir *Directory, width int) string {
	buf := screenBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	writeDirectoryDetails(buf, Dir, width)
	s := buf.String()
	screenBufPool.Put(buf)
	return s
}

func displayFileDetails(fileInfo *FileInfo, width int) string {
	buf := screenBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	writeFileDetails(buf, fileInfo, width)
	s := buf.String()
	screenBufPool.Put(buf)
	return s
}

// writeSortedDirectory writes the visible directory listing into buf.
// currentDir.mu.RLock is held for the duration to prevent concurrent map writes.
func writeSortedDirectory(buf *bytes.Buffer, currentDir *Directory, options []string, selected int, width int) {
	currentDir.mu.RLock()
	defer currentDir.mu.RUnlock()

	for i, option := range options {
		if i == selected {
			buf.WriteString(string(tc.Coral) + "> ")
		} else {
			buf.WriteString("  ")
		}

		if option == "..." {
			buf.WriteString("...\n")
			continue
		}

		if subDir, isDir := currentDir.Subdirectories[strings.TrimSuffix(option, string(os.PathSeparator))]; isDir {
			writeDirectoryDetails(buf, subDir, width)
		} else if fileInfo, isFile := currentDir.Files[option]; isFile {
			writeFileDetails(buf, fileInfo, width)
		}
	}
}

func displaySortedDirectory(currentDir *Directory, options []string, selected int, width int) {
	buf := screenBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	writeSortedDirectory(buf, currentDir, options, selected, width)
	os.Stdout.Write(buf.Bytes())
	screenBufPool.Put(buf)
}

func DirectorySelect(selectedDir *Directory) string {
	option, err := selection.SelectOption(displayDirectoryDetails(selectedDir, 80), GetDirectoryOptions())
	if err != nil {
		println(err.Error())
		return ""
	}
	return option
}

func FileSelect(selectedFile *FileInfo) string {
	option, err := selection.SelectOption(displayFileDetails(selectedFile, 80), GetDirectoryOptions())
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

	removedSize := file.Size
	for d := file.Parent; d != nil; d = d.FileInfo.Parent {
		d.mu.Lock()
		d.Size -= removedSize
		d.SubObjectCount--
		d.mu.Unlock()
	}

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
		err := Deletion(&dir.FileInfo, displayDirectoryDetails(dir, 80), true)
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
		err := Deletion(selectedFile, displayFileDetails(selectedFile, 80), false)
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
	addedSize := newFile.Size
	for d := targetDir; d != nil; d = d.FileInfo.Parent {
		d.mu.Lock()
		d.Size += addedSize
		d.mu.Unlock()
	}

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
