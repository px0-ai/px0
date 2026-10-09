package main

import (
	iign "px0/internal/ignore"
)

type ruleKind = iign.RuleKind

const (
	rkRegex     = iign.RKRegex
	rkSegEq     = iign.RKSegEq
	rkSegSuffix = iign.RKSegSuffix
	rkPathEq    = iign.RKPathEq
)

type rule = iign.Rule
type ignoreSet = iign.IgnoreSet

var defaultIgnores = iign.DefaultIgnores

func newIgnoreSet(extra []string) *ignoreSet {
	return iign.New(extra)
}

func compilePattern(p string) (rule, bool) {
	return iign.CompilePattern(p)
}

func compilePatternRegex(p string) (rule, bool) {
	return iign.CompilePatternRegex(p)
}

func readGitignore(dir, relDir string) []string {
	return iign.ReadGitignore(dir, relDir)
}
