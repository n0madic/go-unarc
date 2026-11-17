package cli

import "strings"

// Header is printed first, without a newline (HEADER1 NAME).
const Header = "UnArc unpacker"

// helpText is printed when the command line cannot be parsed.
const helpText = "\nUsage: unarc command [options] archive[.arc] [filenames...]\n" +
	"Available commands:\n" +
	"  l - display archive listing\n" +
	"  v - display verbose archive listing\n" +
	"  e - extract files into current directory\n" +
	"  x - extract files with pathnames\n" +
	"  t - test archive integrity\n" +
	"Available options:\n" +
	"  -dp{Path}   - set destination path\n" +
	"  -w{Path}    - set temporary files directory\n" +
	"  -o+         - overwrite existing files\n" +
	"  -o-         - don't overwrite existing files\n" +
	"  --noarcext  - don't add default extension to archive name\n" +
	"  -p{Pwd}     - password of an encrypted archive (or env UNARC_PASSWORD)\n" +
	"  -cls{Path}  - folder with the setup's CLS-*.dll, run through clshost.exe under Wine\n" +
	"                (or env UNARC_CLS_DIR; srep runs natively: srep beside unarc, or env UNARC_SREP)\n" +
	"  --          - no more options\n"

// Command is a parsed command line.
type Command struct {
	Cmd       byte
	Arcname   string
	Filenames []string
	OutDir    string
	WorkDir   string
	Yes, No   bool
	NoArcExt  bool
	Password  *string // -p given (possibly empty)
	CLSDir    *string // -cls given (possibly empty)
}

// List reports whether this is a listing command.
func (c *Command) List() bool { return c.Cmd == 'l' || c.Cmd == 'v' }

// ParseArgs parses the arguments after the program name the way COMMAND::COMMAND does; ok is false when
// the help text has to be shown. Unlike C, "--" really ends the options: everything after it is
// the command, the archive or a file name.
func ParseArgs(args []string) (c Command, ok bool) {
	ok = true
	noOptions := false
	cmdSeen, arcSeen := false, false
	i := 0
	for ; ok && i < len(args); i++ {
		a := args[i]
		if !noOptions && strings.HasPrefix(a, "-") {
			switch {
			case a == "--noarcext":
				c.NoArcExt = true
			case a == "-o+":
				c.Yes = true
			case a == "-o-":
				c.No = true
			case strings.HasPrefix(a, "-dp"):
				c.OutDir = a[3:]
			case strings.HasPrefix(a, "-cls"):
				v := a[4:]
				c.CLSDir = &v
			case strings.HasPrefix(a, "-p"):
				v := a[2:]
				c.Password = &v
			case strings.HasPrefix(a, "-w"):
				c.WorkDir = a[2:]
			case a == "--":
				noOptions = true
			default:
				ok = false
			}
			continue
		}
		switch {
		case !cmdSeen:
			cmdSeen = true
			if a != "" {
				c.Cmd = a[0]
			}
			ok = len(a) == 1
		case !arcSeen:
			arcSeen = true
			c.Arcname = a
		default:
			c.Filenames = args[i:]
			i = len(args)
		}
	}
	ok = ok && cmdSeen && strings.IndexByte("lvtex", c.Cmd) >= 0 && arcSeen
	return c, ok
}
