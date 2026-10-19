// Command sonolus-notgarupa-effect-generator builds the NotGarupa effect pack
// (see package effect) as a sonolus-pack source tree, a servable pack and the
// clip contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	effect "github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/cli"
)

var version = "dev"

func main() {
	var common cli.Common
	fs := flag.NewFlagSet("sonolus-notgarupa-effect-generator", flag.ExitOnError)
	common.Register(fs)
	var o effect.Options
	fs.StringVar(&o.TapSE, "tapse", "skin00", "tap SE skin bundle (sound/tapseskin/<id>)")
	fs.StringVar(&o.DirectionalSE, "directional-se", "directionalflickskin00", "directional flick SE bundle (fixed in the original)")
	format := fs.String("format", "wav", "clip format: wav (gapless-trimmed PCM, exact timing) or mp3 (original Bestdori bytes)")
	fs.StringVar(&o.Name, "name", "", "effect item name (default notgarupa-<tapse>)")
	contract := fs.String("contract", "contract.json", "write the clip contract JSON here (empty: skip)")
	_ = fs.Parse(os.Args[1:])
	if common.Version {
		fmt.Println(version)
		return
	}
	o.Format = effect.Format(*format)
	o.Source = effect.Source{CacheDir: common.Cache, Regions: common.Regions, Offline: common.Offline, Log: common.Logf()}

	r, err := effect.Generate(context.Background(), o)
	if err != nil {
		cli.Fatal(err)
	}
	if err := effect.WriteSource(common.Out, r); err != nil {
		cli.Fatal(err)
	}
	if *contract != "" {
		if err := cli.WriteJSON(*contract, r.Contract); err != nil {
			cli.Fatal(err)
		}
	}
	if common.Pack != "" {
		if err := cli.Pack(common.Out, common.Pack); err != nil {
			cli.Fatal(err)
		}
	}
	fmt.Printf("effect %s: %d clips -> %s\n", r.Item.Name, len(r.Data.Clips), common.Out)
}
