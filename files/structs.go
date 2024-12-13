package files

var Root *Directory

//type FileSystem interface {
//	GetOwner(fi os.FileInfo) string
//	GetGroup(fi os.FileInfo) string
//	displayFileDetails(fileInfo FileInfo)
//}
//
//type DirectorySystem interface {
//}

type FileInfo struct {
	Name        string
	Permissions string
	Owner       string
	Group       string
	Size        int64
	ModTime     int64
	FullPath    string
	Target      string
	Parent      *Directory
}

type Directory struct {
	FileInfo
	Subdirectories map[string]*Directory
	Files          map[string]*FileInfo
	SubObjectCount int64
}

type Volume struct {
	Label          string
	FileSystemType string
	TotalSize      int64
	FreeSpace      int64
	RootDirectory  *Directory
	MountPoint     string
}

type Options string

const (
	Backup      Options = "Backup"
	Compare     Options = "Compare"
	Copy        Options = "Copy"
	Delete      Options = "Delete"
	Move        Options = "Move"
	Ownership   Options = "Ownership"
	Permissions Options = "Permissions"
	Rename      Options = "Rename"
	SimLink     Options = "SimLink"
	Stat        Options = "Stat"
	Zip         Options = "Zip"
	Grep        Options = "Grep"
)

func GetDirectoryOptions() []string {
	return []string{
		string(Backup),
		string(Compare),
		string(Copy),
		string(Delete),
		string(Move),
		string(Ownership),
		string(Permissions),
		string(Rename),
		string(SimLink),
		string(Stat),
		string(Zip),
	}
}
