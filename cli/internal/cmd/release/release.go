// Package release implementa `gs release` (REL1): le release sono risorse
// del repository, quindi ogni comando richiede un target owner/repo o -R.
package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	gitstack "github.com/fathorMB/GitStack/client/go"
)

var fields = []string{"id", "tagName", "name", "body", "draft", "prerelease", "latest", "createdAt", "publishedAt", "assets"}

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "release", Short: "Gestisci le release di un repository", Args: cmdutil.NoArgs, RunE: cmdutil.GroupRun}
	c.AddCommand(newList(f), newView(f), newCreate(f), newUpload(f), newDownload(f))
	return c
}
func target(f *cmdutil.Factory, args []string) (string, string, error) {
	v := f.RepoFlag
	if len(args) > 0 {
		v = args[0]
	}
	p := strings.Split(v, "/")
	if len(p) != 2 || p[0] == "" || p[1] == "" {
		return "", "", cmdutil.UsageErrorf("specifica il repository come owner/repo o con -R")
	}
	return p[0], p[1], nil
}
func client(f *cmdutil.Factory) (*api.Client, error) { return api.FromFactory(f) }
func parseID(v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return id, cmdutil.UsageErrorf("id release non valido: %q", v)
	}
	return id, nil
}
func releasePath(o, n string) string  { return "/repos/" + o + "/" + n + "/releases" }
func decode(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }
func newList(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	var limit int
	c := &cobra.Command{Use: "list [<owner>/<repo>]", Aliases: []string{"ls"}, Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, a []string) error {
		o, n, e := target(f, a)
		if e != nil {
			return e
		}
		cl, e := client(f)
		if e != nil {
			return e
		}
		resp, e := cl.Do(c.Context(), http.MethodGet, releasePath(o, n), nil, nil)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		var v gitstack.ReleaseList
		if e = decode(resp.Body, &v); e != nil {
			return e
		}
		if limit > 0 && len(v.Items) > limit {
			v.Items = v.Items[:limit]
		}
		if jo.Enabled() {
			return jo.Write(f.IO.Out, v.Items, f.IO.OutTTY)
		}
		t := output.NewTable(f.IO.OutTTY, f.IO.Width, "ID", "TAG", "NOME", "STATO")
		for _, x := range v.Items {
			st := "pubblicata"
			if x.Draft {
				st = "bozza"
			}
			if x.Prerelease {
				st = "pre-release"
			}
			t.AddRow(x.Id.String(), x.TagName, x.Name, st)
		}
		return t.Render(f.IO.Out)
	}}
	c.Flags().IntVarP(&limit, "limit", "L", 30, "Numero massimo di release")
	output.AddJSONFlags(c, &jo, fields)
	return c
}
func newView(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	c := &cobra.Command{Use: "view <release-id> [<owner>/<repo>]", Args: cobra.RangeArgs(1, 2), RunE: func(c *cobra.Command, a []string) error {
		id, e := parseID(a[0])
		if e != nil {
			return e
		}
		o, n, e := target(f, a[1:])
		if e != nil {
			return e
		}
		cl, e := client(f)
		if e != nil {
			return e
		}
		resp, e := cl.Do(c.Context(), http.MethodGet, releasePath(o, n)+"/"+id.String(), nil, nil)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		var v gitstack.Release
		if e = decode(resp.Body, &v); e != nil {
			return e
		}
		if jo.Enabled() {
			return jo.Write(f.IO.Out, v, f.IO.OutTTY)
		}
		_, e = fmt.Fprintf(f.IO.Out, "%s (%s)\\n%s\\n", v.Name, v.TagName, v.Body)
		return e
	}}
	output.AddJSONFlags(c, &jo, fields)
	return c
}
func newCreate(f *cmdutil.Factory) *cobra.Command {
	var tag, name, body string
	var draft, pre, notes bool
	c := &cobra.Command{Use: "create [<owner>/<repo>]", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, a []string) error {
		o, n, e := target(f, a)
		if e != nil {
			return e
		}
		if tag == "" || name == "" {
			return cmdutil.UsageErrorf("--tag e --name sono obbligatori")
		}
		in := gitstack.CreateReleaseInput{TagName: tag, Name: name, Draft: &draft, Prerelease: &pre, GenerateNotes: &notes}
		if body != "" {
			in.Body = &body
		}
		b, _ := json.Marshal(in)
		cl, e := client(f)
		if e != nil {
			return e
		}
		resp, e := cl.Do(c.Context(), http.MethodPost, releasePath(o, n), bytes.NewReader(b), nil)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		var v gitstack.Release
		if e = decode(resp.Body, &v); e != nil {
			return e
		}
		_, e = fmt.Fprintln(f.IO.Out, v.Id.String())
		return e
	}}
	c.Flags().StringVar(&tag, "tag", "", "Tag Git")
	c.Flags().StringVar(&name, "name", "", "Titolo")
	c.Flags().StringVar(&body, "notes", "", "Note Markdown")
	c.Flags().BoolVar(&notes, "generate-notes", false, "Genera la bozza delle note")
	c.Flags().BoolVar(&draft, "draft", false, "Crea come bozza")
	c.Flags().BoolVar(&pre, "prerelease", false, "Crea come pre-release")
	return c
}
func newUpload(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "upload <release-id> <file> [<owner>/<repo>]", Args: cobra.RangeArgs(2, 3), RunE: func(c *cobra.Command, a []string) error {
		id, e := parseID(a[0])
		if e != nil {
			return e
		}
		o, n, e := target(f, a[2:])
		if e != nil {
			return e
		}
		file, e := os.Open(a[1])
		if e != nil {
			return e
		}
		defer file.Close()
		cl, e := client(f)
		if e != nil {
			return e
		}
		h := http.Header{"Content-Type": []string{"application/octet-stream"}}
		resp, e := cl.Do(c.Context(), http.MethodPost, releasePath(o, n)+"/"+id.String()+"/assets?name="+url.QueryEscape(filepath.Base(a[1])), file, h)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		return nil
	}}
	return c
}
func newDownload(f *cmdutil.Factory) *cobra.Command {
	var out string
	c := &cobra.Command{Use: "download <release-id> <asset-id> [<owner>/<repo>]", Args: cobra.RangeArgs(2, 3), RunE: func(c *cobra.Command, a []string) error {
		id, e := parseID(a[0])
		if e != nil {
			return e
		}
		asset, e := parseID(a[1])
		if e != nil {
			return e
		}
		o, n, e := target(f, a[2:])
		if e != nil {
			return e
		}
		if out == "" {
			return cmdutil.UsageErrorf("--output è obbligatorio")
		}
		cl, e := client(f)
		if e != nil {
			return e
		}
		resp, e := cl.Do(c.Context(), http.MethodGet, releasePath(o, n)+"/"+id.String()+"/assets/"+asset.String()+"/download", nil, nil)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		dst, e := os.Create(out)
		if e != nil {
			return e
		}
		_, e = io.Copy(dst, resp.Body)
		cerr := dst.Close()
		if e != nil {
			return e
		}
		return cerr
	}}
	c.Flags().StringVarP(&out, "output", "o", "", "File di destinazione")
	return c
}
