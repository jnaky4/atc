Alongi Terminal Control

An alternative view to navigating your files and directories with keyboard arrows displaying more relevant color coded human readable data, for rapid visual uptake and navigational ease. doing task you normally wish were in the terminal. Displaying things like accurately calculated folder and file data size, permissions and selecting options for convenience like zip and backup, or open in your default ide.

uses async multithreading to preload parent directories for performance
uses parallelized batches to optimize multithread usage
uses bfs directory recursion to iterate through directory structure
uses caching to save previous calculations to improve performance
uses depth aware batch sizing by caculating thread spawning

avoids external packaging to instead use golangs built in packages

go run main.go
