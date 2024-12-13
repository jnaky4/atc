package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
)

func main() {

	singleCommand()
	//pipeCommand()

}

func singleCommand() {
	//windows
	cmd1 := exec.Command("cmd", "/C", "dir")
	//mac
	//cmd1 := exec.Command("ls", "/Users/Z004X7X/")
	out, _ := cmd1.Output()
	println(string(out))
}

func pipeCommand() {
	//windows
	//fuck windows
	//mac
	first := exec.Command("ls", "c/Users/'Hyperlight Drifter'/Documents/github")
	second := exec.Command("grep", "furnace")

	// http://golang.org/pkg/io/#Pipe

	reader, writer := io.Pipe()

	// push first command output to writer
	first.Stdout = writer

	// read from first command output
	second.Stdin = reader

	// prepare a buffer to capture the output
	// after second command finished executing
	var buff bytes.Buffer
	second.Stdout = &buff

	_ = first.Start()
	_ = second.Start()
	_ = first.Wait()
	_ = writer.Close()
	_ = second.Wait()

	total := buff.String() // convert output to string

	fmt.Printf("Out : %s", total)
}
