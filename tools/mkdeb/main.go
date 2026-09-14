package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: mkdeb <output.deb> <debian-binary> <control.tar.gz> <data.tar.gz>")
		os.Exit(2)
	}
	out, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer out.Close()
	if _, err := out.WriteString("!<arch>\n"); err != nil {
		panic(err)
	}
	for _, path := range os.Args[2:] {
		if err := writeMember(out, path); err != nil {
			panic(err)
		}
	}
}

func writeMember(out io.Writer, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	name := filepath.Base(path)
	if len(name) > 15 {
		return fmt.Errorf("ar member name too long: %s", name)
	}
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name+"/", info.ModTime().Unix(), 0, 0, 0644, info.Size())
	if len(header) != 60 {
		return fmt.Errorf("invalid ar header length for %s: %d", name, len(header))
	}
	if _, err := out.Write([]byte(header)); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(out, file); err != nil {
		return err
	}
	if info.Size()%2 != 0 {
		_, err = out.Write([]byte{'\n'})
	}
	return err
}
