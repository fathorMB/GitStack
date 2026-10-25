// Package pkg implementa `gs package`: le immagini OCI del registry di un
// repository (RG1). Elenca ed elimina; la pubblicazione passa da docker o
// dalla CI.
package pkg

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/api"
	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
	"github.com/fathorMB/GitStack/cli/internal/output"
	gitstack "github.com/fathorMB/GitStack/client/go"
)

var fields = []string{"name", "tags", "sizeBytes", "updatedAt", "digest", "signature", "hasSbom", "kind", "pushedBy", "tagDetails"}

func NewCmd(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "package", Aliases: []string{"pkg"}, Short: "Gestisci le immagini del registry di un repository", Args: cmdutil.NoArgs, RunE: cmdutil.GroupRun}
	c.AddCommand(newList(f), newDelete(f))
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

func path(o, n string) string { return "/repos/" + o + "/" + n + "/packages" }

func newList(f *cmdutil.Factory) *cobra.Command {
	var jo output.JSONOptions
	c := &cobra.Command{Use: "list [<owner>/<repo>]", Aliases: []string{"ls"}, Short: "Elenca le immagini con tag, firma e SBOM", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, a []string) error {
		o, n, e := target(f, a)
		if e != nil {
			return e
		}
		cl, e := api.FromFactory(f)
		if e != nil {
			return e
		}
		resp, e := cl.Do(c.Context(), http.MethodGet, path(o, n), nil, nil)
		if e != nil {
			return e
		}
		defer func() { _ = resp.Body.Close() }()
		var v gitstack.PackageList
		if e = json.NewDecoder(resp.Body).Decode(&v); e != nil {
			return e
		}
		if jo.Enabled() {
			return jo.Write(f.IO.Out, v.Items, f.IO.OutTTY)
		}
		t := output.NewTable(f.IO.OutTTY, f.IO.Width, "IMMAGINE", "TAG", "FIRMA", "SBOM", "DIMENSIONE")
		for _, x := range v.Items {
			sbom := "no"
			if x.HasSbom {
				sbom = "sì"
			}
			t.AddRow(x.Name, strings.Join(x.Tags, ","), string(x.Signature), sbom, fmt.Sprintf("%d", x.SizeBytes))
		}
		return t.Render(f.IO.Out)
	}}
	output.AddJSONFlags(c, &jo, fields)
	return c
}

func newDelete(f *cmdutil.Factory) *cobra.Command {
	var tag string
	var yes bool
	c := &cobra.Command{Use: "delete <immagine> [<owner>/<repo>]", Aliases: []string{"rm"}, Short: "Elimina un'immagine o un suo tag (chiede conferma)", Args: cobra.RangeArgs(1, 2), RunE: func(c *cobra.Command, a []string) error {
		o, n, e := target(f, a[1:])
		if e != nil {
			return e
		}
		what := "l'immagine " + a[0]
		if tag != "" {
			what = "il tag " + tag + " di " + a[0]
		}
		if e = cmdutil.ConfirmOrYes(f.IO, yes, fmt.Sprintf("Eliminare %s. Riscrivi `%s` per confermare:", what, a[0]), a[0]); e != nil {
			return e
		}
		cl, e := api.FromFactory(f)
		if e != nil {
			return e
		}
		q := url.Values{"name": {a[0]}}
		if tag != "" {
			q.Set("tag", tag)
		}
		resp, e := cl.Do(c.Context(), http.MethodDelete, path(o, n)+"?"+q.Encode(), nil, nil)
		if e != nil {
			return e
		}
		_ = resp.Body.Close()
		_, e = fmt.Fprintf(f.IO.Out, "Eliminato %s\n", what)
		return e
	}}
	c.Flags().StringVar(&tag, "tag", "", "Elimina solo questo tag")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "Conferma senza chiedere")
	return c
}
