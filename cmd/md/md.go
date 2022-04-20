package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/cli/cli/pkg/iostreams"
	"github.com/yuin/goldmark"
	goldmarkemoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/ast"
	goldmarkextension "github.com/yuin/goldmark/extension"
	astext "github.com/yuin/goldmark/extension/ast"
	goldmarkrenderer "github.com/yuin/goldmark/renderer"
	goldmarkutil "github.com/yuin/goldmark/util"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	io := iostreams.System()

	b, err := ioutil.ReadAll(io.In)
	if err != nil {
		return err
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			goldmarkextension.GFM,
			goldmarkextension.DefinitionList,
			goldmarkemoji.Emoji,
		),
	)
	r := renderer{}
	md.SetRenderer(goldmarkrenderer.NewRenderer(goldmarkrenderer.WithNodeRenderers(
		goldmarkutil.Prioritized(&r, 0),
	)))

	buf := bytes.Buffer{}
	if err := md.Convert(b, &buf); err != nil {
		return fmt.Errorf("goldmark error: %w", err)
	}

	io.SetPager("less")
	_ = io.StartPager()
	_, err = fmt.Fprint(io.Out, buf.String())
	io.StopPager()
	return err
}

type renderer struct {
	prevText []byte
}

func (r *renderer) RegisterFuncs(reg goldmarkrenderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindDocument, r.renderBlock)
	reg.Register(ast.KindHeading, r.renderBlock)
	// reg.Register(goldmarkast.KindBlockquote, r.renderBlockquote)
	// reg.Register(goldmarkast.KindCodeBlock, r.renderCodeBlock)
	reg.Register(ast.KindFencedCodeBlock, r.renderCodeBlock)
	// reg.Register(goldmarkast.KindHTMLBlock, r.renderHTMLBlock)
	reg.Register(ast.KindParagraph, r.renderBlock)
	reg.Register(ast.KindTextBlock, r.renderBlock)
	reg.Register(ast.KindList, r.renderBlock)
	reg.Register(ast.KindListItem, r.renderBlock)
	// reg.Register(goldmarkast.KindThematicBreak, r.renderThematicBreak)

	reg.Register(ast.KindText, r.renderText)
	reg.Register(ast.KindAutoLink, r.renderSpan)
	reg.Register(ast.KindCodeSpan, r.renderSpan)
	reg.Register(ast.KindEmphasis, r.renderSpan)
	// reg.Register(goldmarkast.KindImage, r.renderImage)
	// reg.Register(goldmarkast.KindLink, r.renderLink)
	// reg.Register(goldmarkast.KindRawHTML, r.renderGeneric)
	// reg.Register(goldmarkast.KindString, r.renderString)

	reg.Register(astext.KindDefinitionList, r.renderBlock)
	reg.Register(astext.KindDefinitionTerm, r.renderBlock)
	reg.Register(astext.KindDefinitionDescription, r.renderBlock)
}

func (r *renderer) renderBlock(w goldmarkutil.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	var err error
	if entering {
		r.prevText = nil
	}

	switch n.Kind() {
	case ast.KindDocument, astext.KindDefinitionList, ast.KindList, ast.KindTextBlock, ast.KindListItem:
	case ast.KindParagraph, ast.KindHeading, astext.KindDefinitionTerm:
		if entering {
			_, err = w.WriteString("\n\n")
		}
	case astext.KindDefinitionDescription:
		if entering {
			_, err = w.WriteString("\n    ")
		}
	default:
		return ast.WalkContinue, fmt.Errorf("unknown block type: %q", n.Kind())
	}

	switch n.Kind() {
	case ast.KindHeading:
		return r.renderHeading(w, source, n, entering)
	}

	return ast.WalkContinue, err
}

func (r *renderer) renderCodeBlock(w goldmarkutil.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	var err error
	walkStatus := ast.WalkContinue
	if entering {
		if _, err = w.WriteString("\n\n"); err != nil {
			return walkStatus, err
		}
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			l := lines.At(i)
			if _, err = w.WriteString("    "); err != nil {
				return walkStatus, err
			}
			if _, err = w.Write(l.Value(source)); err != nil {
				return walkStatus, err
			}
		}
	}
	return walkStatus, err
}

func (r *renderer) renderGeneric(w goldmarkutil.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	fmt.Fprintf(w, "render: %s (%v)\n", n.Kind(), entering)
	return ast.WalkContinue, nil
}

func (r *renderer) renderText(w goldmarkutil.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	var err error
	if entering {
		t := n.Text(source)
		// FIXME: figure out how to insert a space only when needed
		if len(r.prevText) > 0 && r.prevText[len(r.prevText)-1] != ' ' && len(t) > 0 && t[0] != ' ' {
			if _, err = w.WriteRune(' '); err != nil {
				return ast.WalkContinue, err
			}
		}
		r.prevText = t
		_, err = w.Write(t)
	}
	return ast.WalkContinue, err
}

func (r *renderer) renderHeading(w goldmarkutil.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	var err error
	if entering {
		_, err = w.WriteString("\x1b[1;34m")
	} else {
		_, err = w.WriteString("\x1b[m")
	}
	return ast.WalkContinue, err
}

func (r *renderer) renderSpan(w goldmarkutil.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	var err error
	if entering {
		_, err = w.WriteString("\x1b[33m")
	} else {
		_, err = w.WriteString("\x1b[m")
	}
	return ast.WalkContinue, err
}
