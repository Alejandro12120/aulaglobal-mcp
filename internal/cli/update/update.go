package update

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"aulaglobal-mcp/internal/selfupdate"
)

const updateUsage = `Uso: aulaglobal-mcp update [--check] [--yes] [--force]

Busca la última versión publicada en GitHub y, si es más reciente, la instala
sustituyendo este programa. Antes de instalar se verifica la firma de la
versión y el checksum del archivo descargado.

  --check   solo comprueba si hay una versión nueva, sin instalar
  --yes     instala sin pedir confirmación
  --force   permite actualizar una compilación local (versión «dev»)
`

// Run is the `aulaglobal-mcp update` command. version is the version of the
// running program.
func Run(args []string, version string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return runUpdate(ctx, args, os.Stdin, os.Stdout, selfupdate.New(version), selfupdate.InContainer())
}

func runUpdate(ctx context.Context, args []string, in io.Reader, out io.Writer, u *selfupdate.Updater, inContainer bool) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	check := flags.Bool("check", false, "")
	yes := flags.Bool("yes", false, "")
	force := flags.Bool("force", false, "")
	if e := flags.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			fmt.Fprint(out, updateUsage)
			return nil
		}
		return fmt.Errorf("%w\n\n%s", e, updateUsage)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("argumento inesperado %q\n\n%s", flags.Arg(0), updateUsage)
	}
	if inContainer {
		return errors.New("este programa se ejecuta en un contenedor y no se actualiza por sí mismo; descarga la imagen nueva (docker pull) y vuelve a crear el contenedor")
	}

	current, comparable := selfupdate.ParseVersion(u.Current)
	if comparable {
		fmt.Fprintf(out, "· Versión instalada: %s\n", current)
	} else {
		fmt.Fprintf(out, "· Versión instalada: %s (compilación local, no comparable)\n", u.Current)
		if !*check && !*force {
			return errors.New("esta es una compilación local y no se puede comparar con las versiones publicadas; usa --force para instalar de todos modos la última versión publicada")
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	rel, e := u.Latest(ctx)
	if e != nil {
		return fmt.Errorf("no se pudo consultar la última versión: %w", e)
	}
	latest := rel.Version()
	fmt.Fprintf(out, "· Última versión publicada: %s\n", latest)
	if comparable {
		switch c := latest.Compare(current); {
		case c == 0:
			fmt.Fprintln(out, "✓ Ya tienes la última versión.")
			return nil
		case c < 0:
			fmt.Fprintln(out, "✓ La versión instalada es más reciente que la publicada; no se cambia nada.")
			return nil
		}
	}
	if *check {
		if comparable {
			fmt.Fprintf(out, "! Hay una versión nueva (%s → %s). Ejecuta `aulaglobal-mcp update` para instalarla.\n", current, latest)
		}
		return nil
	}
	if !*yes && !confirm(in, out, fmt.Sprintf("¿Instalar %s? [S/n]", latest)) {
		fmt.Fprintln(out, "· Actualización cancelada.")
		return nil
	}

	if e := u.Install(ctx, rel, func(s string) { fmt.Fprintln(out, s) }); e != nil {
		return e
	}
	fmt.Fprintf(out, "✓ Actualizado: %s → %s\n", strings.TrimSpace(u.Current), latest)
	fmt.Fprintln(out, "· Reinicia el asistente para usar la versión nueva.")
	return nil
}

// confirm asks a yes/no question; Enter means yes, closed input means no.
func confirm(in io.Reader, out io.Writer, q string) bool {
	fmt.Fprint(out, "\n› "+q+" ")
	line, err := bufio.NewReader(io.LimitReader(in, 1024)).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "s", "si", "sí", "y", "yes":
		return true
	}
	return false
}
