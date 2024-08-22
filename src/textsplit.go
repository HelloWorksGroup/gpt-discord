package main

import (
	"regexp"
	"strings"
)

func simpleMarkdownBlockSplitter(md string) (markdownSplitted []string) {
	const maxSplitLength = 1920
	const maxBlockLength = 1000
	markdownBlocks := make([]string, 0)
	markdownSplitted = make([]string, 0)
	currentBlock := ""
	codeBlock := false

	newBlockBefore := func(line string) {
		if currentBlock != "" {
			markdownBlocks = append(markdownBlocks, currentBlock)
		}
		currentBlock = line + "\n"
	}
	newBlockAfter := func(line string) {
		currentBlock += line + "\n"
		markdownBlocks = append(markdownBlocks, currentBlock)
		currentBlock = ""
	}

	lines := strings.Split(md, "\n")
	for _, line := range lines {

		if codeBlock && regexp.MustCompile("^\\s*```").MatchString(line) {
			codeBlock = false
			newBlockAfter(line)
			continue
		}

		if !codeBlock {
			if regexp.MustCompile("^\\s*```\\w*").MatchString(line) {
				codeBlock = true
				newBlockBefore(line)
				continue
			} else {
				if regexp.MustCompile("^#+").MatchString(line) {
					newBlockBefore(line)
					continue
				}
				if len(currentBlock) > maxBlockLength {
					newBlockBefore(line)
					continue
				}
			}
		}
		currentBlock += line + "\n"
	}
	newBlockBefore("")

	output := ""
	for _, v := range markdownBlocks {
		if len(output)+len(v) < maxSplitLength {
			output += v
		} else {
			markdownSplitted = append(markdownSplitted, output)
			output = v
		}
	}
	markdownSplitted = append(markdownSplitted, output)
	return
}
