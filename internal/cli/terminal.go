package cli

import (
	"io"
	"os"
)

func colorful(output io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func palette(output io.Writer) func(code, text string) string {
	on := colorful(output)
	return func(code, text string) string {
		if !on {
			return text
		}
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
}
