// Command wzexport copies images out of a game client's .wz archives as the
// XML files the server reads from its data root.
//
//	wzexport -client <client folder> -out <folder> [-keep info,life] Map.wz/Map/Map9/915000000.img ...
//
// The last part of a path may be a pattern: Skill.wz/[0-9]*.img is every job's
// skill book.
//
// Each image is written to <out>/<archive>/<path>.xml. -keep limits an image
// to the named top-level entries.
package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"maple-go-server/internal/wz"
)

func main() {
	client := flag.String("client", os.Getenv("MAPLEGO_CLIENT_WZ_ROOT"), "game client folder holding the .wz archives")
	out := flag.String("out", "", "folder to write the XML tree into")
	keepList := flag.String("keep", "", "comma separated top-level entries to keep (default: all)")
	flag.Parse()
	if *client == "" || *out == "" || flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	var keep func(string) bool
	if *keepList != "" {
		names := map[string]bool{}
		for _, n := range strings.Split(*keepList, ",") {
			names[n] = true
		}
		keep = func(n string) bool { return names[n] }
	}
	archives := map[string]*wz.Archive{}
	for _, arg := range flag.Args() {
		arg = strings.Trim(filepath.ToSlash(arg), "/")
		file, inner, ok := strings.Cut(arg, "/")
		if !ok || !strings.HasSuffix(file, ".wz") {
			fail(fmt.Errorf("%s: want <archive>.wz/<path>.img", arg))
		}
		a := archives[file]
		if a == nil {
			var err error
			if a, err = wz.Open(filepath.Join(*client, file)); err != nil {
				fail(err)
			}
			defer a.Close()
			archives[file] = a
		}
		inners := []string{inner}
		if dir, pattern := path.Split(inner); strings.ContainsAny(pattern, "*?[") {
			// The last part may be a pattern: Skill.wz/[0-9]*.img is every job's book.
			names, err := a.Images(dir)
			if err != nil {
				fail(fmt.Errorf("%s: %w", arg, err))
			}
			inners = nil
			for _, name := range names {
				if ok, err := path.Match(pattern, name); err != nil {
					fail(fmt.Errorf("%s: %w", arg, err))
				} else if ok {
					inners = append(inners, dir+name)
				}
			}
		}
		for _, inner := range inners {
			data, err := a.ExportXML(inner, keep)
			if err != nil {
				fail(fmt.Errorf("%s/%s: %w", file, inner, err))
			}
			target := filepath.Join(*out, file, filepath.FromSlash(inner)+".xml")
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				fail(err)
			}
			if err = os.WriteFile(target, data, 0o644); err != nil {
				fail(err)
			}
			fmt.Println(target, len(data))
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "wzexport:", err)
	os.Exit(1)
}
