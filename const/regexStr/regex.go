package regexStr

const (
	Octal              = `^[0-7]{3}$`
	UnixAbsFilePath    = `^\/([a-zA-Z0-9_-]+\/)*[a-zA-Z0-9._-]+$`
	WindowsABSFilePath = `^"?[A-Za-z]:\\(?:[^\\\"<>\|:*?/]+\\)*[^\\\"<>\|:*?/]+\.[a-zA-Z0-9]+\"?$`

	WindowsRoot = `^[A-Za-z]:`
	UnixRoot    = `^/`

	UnixDirectory    = `\/(?:[a-zA-Z0-9_\s()-]+\/|[a-zA-Z0-9_-]+\/)*`
	WindowsDirectory = `\\(?:[a-zA-Z0-9-__\s()]+\\)+`

	UnixRelative    = `^[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9._-]+)*$`
	WindowsRelative = `^(?:[^\\\"<>\|:*?/]+\\)*[^\\\"<>\|:*?/]+$`

	UnixFile    = `(\.[a-zA-Z0-9._-]+|[a-zA-Z0-9._-]+)$`
	WindowsFile = `^[^\\\"<>\|:*?/]+(?:\.[a-zA-Z0-9]+)?$`
)
