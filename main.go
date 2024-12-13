package main

import (
	"filesystem/files"
	"fmt"
	"os"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	if len(os.Args) > 1 {
		cwd = os.Args[1]

	}

	startDir, err := files.BuildDirectoryStructure(cwd)
	if err != nil {
		println(err.Error())
		return
	}

	files.Root = startDir //todo remove

	for {
		selectedDir, selectedFile, err := files.DisplayDirectoryNavigation(startDir)
		if err != nil {
			println(err.Error())
			return
		}

		if selectedDir != nil {
			files.HandleDirOperation(files.DirectorySelect(selectedDir), selectedDir)
		} else {
			files.HandleFileOperation(files.FileSelect(selectedFile), selectedFile)
		}
	}

	//TODO SCP to file, run file and delete itself

}
