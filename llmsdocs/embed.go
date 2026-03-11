// Package llmsdocs embeds framework reference files for scaffolded projects.
package llmsdocs

import _ "embed"

//go:embed llms.txt
var LLMsTxt []byte

//go:embed llms-full.txt
var LLMsFullTxt []byte
