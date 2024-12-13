package files

//
//import (
//	"bufio"
//	"fmt"
//	"os"
//	"path/filepath"
//	"strings"
//)
//
//func ParseDirectoryStructure(output string) Directory {
//	scanner := bufio.NewScanner(strings.NewReader(output))
//	var allLines []string
//
//	for scanner.Scan() {
//		allLines = append(allLines, scanner.Text())
//	}
//
//	////for _, line := range allLines {
//	////	println(line)
//	////}
//	////println()
//
//	*Root = parseAll(allLines)
//
//	return *Root
//}
//
//func parseAll(remainingLines []string) Directory {
//	currentDir := &Directory{
//		Files:          make(map[string]FileInfo),
//		Subdirectories: make(map[string]*Directory),
//		Links:          make(map[string]Link),
//		Parent:         &Directory{},
//	}
//
//	for i, line := range remainingLines {
//		if i == 0 {
//			currentDir.Name = Root.Name
//			Root = currentDir
//			Root.Parent = &Directory{
//				Name:           filepath.Dir(Root.Name),
//				Files:          make(map[string]FileInfo),
//				Subdirectories: make(map[string]*Directory),
//				Links:          make(map[string]Link),
//			}
//		}
//		if skip(line) {
//			continue
//		}
//
//		//Indicates new directory/file header
//		if strings.HasSuffix(line, ":") {
//			name := strings.TrimSuffix(line, ":")
//
//			if i == 0 { //if this is the first time?
//				continue
//			} else { //root has to be set
//				if currentDir.Size == "" {
//					currentDir = &Directory{
//						Name:           name,
//						Files:          make(map[string]FileInfo),
//						Subdirectories: make(map[string]*Directory),
//						Links:          make(map[string]Link),
//						Parent:         &Directory{Name: filepath.Dir(name)}, //strip a / from the route
//					}
//				}
//				currentDir = FindDirectoryByName(Root, name)
//
//			}
//
//			continue
//		}
//
//		//Current directory details
//		if strings.HasSuffix(line, ".") {
//			tmp := ParseDirectory(line)
//
//			currentDir.Group = tmp.Group
//			currentDir.Permissions = tmp.Permissions
//			currentDir.Owner = tmp.Owner
//			currentDir.Size = tmp.Size
//
//			continue
//		}
//
//		//we found a subdirectory, add to currents sub
//		if strings.HasPrefix(line, "d") {
//			subDir := ParseDirectory(line)
//			subDir.Name = fmt.Sprintf("%s/%s", currentDir.Name, subDir.Name)
//			subDir.Parent = currentDir
//			currentDir.Subdirectories[subDir.Name] = &subDir
//			continue
//		}
//
//		if strings.HasPrefix(line, "-") {
//			subFile := parseFile(line)
//			subFile.Name = fmt.Sprintf("%s/%s", currentDir.Name, subFile.Name)
//			currentDir.Files[subFile.Name] = subFile
//			continue
//		}
//
//		if strings.HasPrefix(line, "l") {
//			link := ParseLink(line)
//			//link.Name = fmt.Sprintf("%s/%s", currentDir.Name, link.Name)
//			currentDir.Links[link.Name] = link
//			continue
//		}
//
//		println("unrecognized line: ", line, " len: ", len(strings.Fields(line)))
//
//	}
//	return *Root
//}
//
//func ParseDirectory(line string) Directory {
//	var dir Directory
//	fields := strings.Fields(line)
//	if len(fields) >= 9 {
//		dir = Directory{
//			Name:           fields[len(fields)-1],
//			Permissions:    rwxToOctal(fields[0]),
//			Owner:          fields[2],
//			Group:          fields[3],
//			Size:           fields[4],
//			Files:          make(map[string]FileInfo),
//			Subdirectories: make(map[string]*Directory),
//			Links:          make(map[string]Link),
//			Parent:         &Directory{},
//		}
//	}
//	return dir
//
//}
//
//func parseFile(line string) FileInfo {
//	fields := strings.Fields(line)
//	if len(fields) >= 9 {
//		return FileInfo{
//			Name:        fields[len(fields)-1],
//			Permissions: rwxToOctal(fields[0]),
//			Owner:       fields[2],
//			Group:       fields[3],
//			Size:        fields[4],
//		}
//	}
//	return FileInfo{}
//}
//
//func ParseLink(line string) Link {
//	var link Link
//	fields := strings.Fields(line)
//	if len(fields) >= 9 {
//		link = Link{
//			Name:        strings.Join(fields[len(fields)-3:], " "), // Name of the link
//			Permissions: rwxToOctal(fields[0]),                     // Convert permissions to octal
//			Owner:       fields[2],                                 // Owner of the link
//			Group:       fields[3],                                 // Group of the link
//			Size:        fields[4],                                 // Size of the link
//			Target:      fields[8],                                 // Target of the link (assuming it's at index 8)
//		}
//	}
//
//	return link
//}
//
////Helper functions
//
//func FindDirectoryByName(dir *Directory, name string) *Directory {
//	// Check if the current directory is the one we're looking for
//	if dir.Name == name {
//		return dir
//	}
//
//	// If not, recursively search in each subdirectory
//	for _, subDir := range dir.Subdirectories {
//		result := FindDirectoryByName(subDir, name)
//		if result != nil {
//			return result // Return as soon as we find the target directory
//		}
//	}
//
//	// If the directory was not found
//	return nil
//}
//
//func rwxToOctal(perm string) string {
//	octal := 0
//
//	// Iterate over permission characters (starting from index 1)
//	for i := 1; i < len(perm); i++ {
//		switch perm[i] {
//		case 'r':
//			octal += 4
//		case 'w':
//			octal += 2
//		case 'x':
//			octal += 1
//		}
//		// Shift left after every three characters
//		if i%3 == 0 && i != 9 { // Avoid shifting on the last set
//			octal <<= 3
//		}
//	}
//
//	return fmt.Sprintf("%o", octal)
//}
//
//func skip(line string) bool {
//
//	if len(line) == 0 {
//		return true
//	}
//
//	//useless
//	if strings.HasPrefix(line, "total") {
//		return true
//	}
//
//	//skip parent dir details
//	if strings.HasSuffix(line, "..") {
//		return true
//	}
//	return false
//}
//
//func CompareDirectories(dir1, dir2 *Directory) {
//	if dir1.Permissions != dir2.Permissions {
//		fmt.Printf("Directory '%s' permissions differ: Dir1=%s, Dir2=%s\n", dir1.Name, dir1.Permissions, dir2.Permissions)
//	}
//	if dir1.Owner != dir2.Owner {
//		fmt.Printf("Directory '%s' owner differs: Dir1=%s, Dir2=%s\n", dir1.Name, dir1.Owner, dir2.Owner)
//	}
//	if dir1.Group != dir2.Group {
//		fmt.Printf("Directory '%s' group differs: Dir1=%s, Dir2=%s\n", dir1.Name, dir1.Group, dir2.Group)
//	}
//	if dir1.Size != dir2.Size {
//		fmt.Printf("Directory '%s' size differs: Dir1=%s, Dir2=%s\n", dir1.Name, dir1.Size, dir2.Size)
//	}
//}
//
//func CompareRecursion(dir1, dir2 *Directory) {
//	//CompareDirectories(dir1, dir2)
//	//fmt.Println(Red + "This is red text" + Reset)
//
//	//compareFiles(dir1.Files, dir2.Files)
//
//	if strings.Contains(dir1.Name, "unimatrix") {
//		println("found")
//	}
//
//	compareLinks(dir1.Links, dir2.Links)
//
//	for subDirName, subDir1 := range dir1.Subdirectories {
//		subDir2, exists := dir2.Subdirectories[subDirName]
//		if !exists {
//			fmt.Printf("Subdirectory '%s' exists in Dir1 but not in Dir2\n", subDirName)
//			continue
//		}
//		CompareRecursion(subDir1, subDir2)
//	}
//
//	for subDirName := range dir2.Subdirectories {
//		if _, exists := dir1.Subdirectories[subDirName]; !exists {
//			fmt.Printf("Subdirectory '%s' exists in Dir2 but not in Dir1\n", subDirName)
//		}
//	}
//}
//
//// CompareFiles compares the files in two directories
//func compareFiles(files1, files2 map[string]FileInfo) {
//	for fileName, file1 := range files1 {
//		file2, exists := files2[fileName]
//		if !exists {
//			fmt.Printf("File '%s' exists in Dir1 but not in Dir2\n", fileName)
//			continue
//		}
//		if file1.Permissions != file2.Permissions {
//			fmt.Printf("File '%s' permissions differ: Dir1=%s, Dir2=%s\n", fileName, file1.Permissions, file2.Permissions)
//		}
//		if file1.Owner != file2.Owner {
//			fmt.Printf("File '%s' owner differs: Dir1=%s, Dir2=%s\n", fileName, file1.Owner, file2.Owner)
//		}
//		if file1.Group != file2.Group {
//			fmt.Printf("File '%s' group differs: Dir1=%s, Dir2=%s\n", fileName, file1.Group, file2.Group)
//		}
//		if file1.Size != file2.Size {
//			fmt.Printf("File '%s' size differs: Dir1=%s, Dir2=%s\n", fileName, file1.Size, file2.Size)
//		}
//	}
//
//	for fileName := range files2 {
//		if _, exists := files1[fileName]; !exists {
//			fmt.Printf("File '%s' exists in Dir2 but not in Dir1\n", fileName)
//		}
//	}
//}
//
//// CompareLinks compares the links in two directories
//func compareLinks(links1, links2 map[string]Link) {
//	// Check for links that exist in Dir1 but not in Dir2
//	for linkName := range links1 {
//		if _, exists := links2[linkName]; !exists {
//			fmt.Printf("Link '%s' exists in Dir1 but not in Dir2\n", linkName)
//		} else {
//			link2 := links2[linkName]
//			link1 := links1[linkName]
//			// Compare attributes of the links
//			if link1.Permissions != link2.Permissions {
//				fmt.Printf("Link '%s' permissions differ: Dir1=%s, Dir2=%s\n", linkName, link1.Permissions, link2.Permissions)
//			}
//			if link1.Owner != link2.Owner {
//				fmt.Printf("Link '%s' owner differs: Dir1=%s, Dir2=%s\n", linkName, link1.Owner, link2.Owner)
//			}
//			if link1.Group != link2.Group {
//				fmt.Printf("Link '%s' group differs: Dir1=%s, Dir2=%s\n", linkName, link1.Group, link2.Group)
//			}
//			if link1.Size != link2.Size {
//				fmt.Printf("Link '%s' size differs: Dir1=%s, Dir2=%s\n", linkName, link1.Size, link2.Size)
//			}
//			if link1.Target != link2.Target {
//				fmt.Printf("Link '%s' target differs: Dir1=%s, Dir2=%s\n", linkName, link1.Target, link2.Target)
//			}
//		}
//	}
//
//	// Check for links that exist in Dir2 but not in Dir1
//	for linkName := range links2 {
//		if _, exists := links1[linkName]; !exists {
//			fmt.Printf("Link '%s' exists in Dir2 but not in Dir1\n", linkName)
//		}
//	}
//}
//
//func WriteLinesToFile(lines []string, filename string) error {
//	file, err := os.Create(filename)
//	if err != nil {
//		return err
//	}
//	defer file.Close()
//
//	for _, line := range lines {
//		_, err := file.WriteString(line + "\n")
//		if err != nil {
//			return err
//		}
//	}
//	return nil
//}
//
//func writeDirectoryToFile(file *os.File, dir *Directory, indent string) error {
//
//	// LINK
//	for linkName, linkInfo := range dir.Links {
//		_, err := fmt.Fprintf(file, "%s  L:%s/%s %s %s:%s %s\n", indent, dir.Name, linkName, linkInfo.Permissions, linkInfo.Owner, linkInfo.Group, linkInfo.Size)
//		if err != nil {
//			return err
//		}
//	}
//
//	// DIRECTORY
//	_, err := fmt.Fprintf(file, "%sD:%s %s %s:%s %s\n", indent, dir.Name, dir.Permissions, dir.Owner, dir.Group, dir.Size)
//	if err != nil {
//		return err
//	}
//
//	// FILE
//	for fileName, fileInfo := range dir.Files {
//		_, err := fmt.Fprintf(file, "%s  F:%s %s %s:%s %s\n", indent, fileName, fileInfo.Permissions, fileInfo.Owner, fileInfo.Group, fileInfo.Size)
//		if err != nil {
//			return err
//		}
//	}
//
//	// Recurse into subdirectories with increased indentation
//	for _, subDir := range dir.Subdirectories {
//		if strings.Contains(subDir.Name, "unimatrix/") {
//			continue
//		} //todo remove hardcoded filtering of the symlink
//
//		err = writeDirectoryToFile(file, subDir, indent+"  ")
//		if err != nil {
//			return err
//		}
//	}
//
//	return nil
//}
//
//func SaveDirectoryToFile(dir *Directory, filePath string) error {
//	// Create or overwrite the file
//	file, err := os.Create(filePath)
//	if err != nil {
//		return fmt.Errorf("failed to create file: %v", err)
//	}
//
//	defer file.Close()
//
//	// Start writing directory to file
//	err = writeDirectoryToFile(file, dir, "")
//	if err != nil {
//		return fmt.Errorf("failed to write directory to file: %v", err)
//	}
//
//	return nil
//}
