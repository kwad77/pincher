// SPDX-License-Identifier: MIT
// Vendored and adapted from github.com/malivvan/tree-sitter (MIT) — pincher in-tree binding.

package tsbridge

import (
	"context"
	"fmt"
)

type (
	Language struct {
		t TreeSitter
		l uint64
	}

	LanguageError struct {
		version uint64
	}
)

func (l LanguageError) Error() string {
	return fmt.Sprintf("Incompatible language version %d", l.version)
}

func NewLanguage(l uint64, t TreeSitter) Language {
	return Language{l: l, t: t}
}

func (t TreeSitter) LanguageC(ctx context.Context) (Language, error) {
	cLangPtr, err := t.languageC.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating c language: %w", err)
	}
	return NewLanguage(cLangPtr[0], t), nil
}

func (t TreeSitter) LanguageCpp(ctx context.Context) (Language, error) {
	cLangPtr, err := t.languageCpp.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating cpp language: %w", err)
	}
	return NewLanguage(cLangPtr[0], t), nil
}

func (t TreeSitter) LanguageRust(ctx context.Context) (Language, error) {
	p, err := t.languageRust.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating rust language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageJava(ctx context.Context) (Language, error) {
	p, err := t.languageJava.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating java language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageCSharp(ctx context.Context) (Language, error) {
	p, err := t.languageCSharp.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating csharp language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageKotlin(ctx context.Context) (Language, error) {
	p, err := t.languageKotlin.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating kotlin language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageRuby(ctx context.Context) (Language, error) {
	p, err := t.languageRuby.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating ruby language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageSwift(ctx context.Context) (Language, error) {
	p, err := t.languageSwift.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating swift language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguagePHP(ctx context.Context) (Language, error) {
	p, err := t.languagePHP.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating php language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageTypeScript(ctx context.Context) (Language, error) {
	p, err := t.languageTypeScript.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating typescript language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}

func (t TreeSitter) LanguageTSX(ctx context.Context) (Language, error) {
	p, err := t.languageTSX.Call(ctx)
	if err != nil {
		return Language{}, fmt.Errorf("initiating tsx language: %w", err)
	}
	return NewLanguage(p[0], t), nil
}
