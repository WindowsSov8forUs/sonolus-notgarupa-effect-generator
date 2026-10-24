// Command sonolus-notgarupa-effect-generator builds every NotGarupa effect
// item (see package effect) into <out>/source, packs it into <out>/pack and
// writes each item's clip contract to <out>/contracts/<item>.json.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	effect "github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/cli"
)

var version = "dev"

func main() {
	var common cli.Common
	fs := flag.NewFlagSet("sonolus-notgarupa-effect-generator", flag.ExitOnError)
	common.Register(fs)
	assetVersion := fs.String("asset-version", effect.PinnedAssetVersion, "official content CDN asset version of the cue sheets")
	workers := fs.Int("j", 0, "items built in parallel (0: one per CPU)")
	_ = fs.Parse(os.Args[1:])
	if common.Version {
		fmt.Println(version)
		return
	}
	opts := effect.Options{
		Source: effect.Source{CacheDir: common.Cache, Regions: common.Regions, AssetVersion: *assetVersion,
			Offline: common.Offline, Log: common.Logf()},
		Workers: *workers,
	}
	source, pack, contracts := filepath.Join(common.Out, "source"), filepath.Join(common.Out, "pack"), filepath.Join(common.Out, "contracts")
	tree, err := effect.CreateSourceTree(source)
	if err != nil {
		cli.Fatal(err)
	}
	if err := os.RemoveAll(contracts); err != nil {
		cli.Fatal(err)
	}
	count := 0
	err = effect.Generate(context.Background(), opts, func(r *effect.Result) error {
		if err := tree.Add(r); err != nil {
			return err
		}
		if err := cli.WriteJSON(filepath.Join(contracts, r.Item.Name+".json"), r.Contract); err != nil {
			return err
		}
		count++
		hold := "once"
		if r.Contract.Hold.Loop {
			hold = fmt.Sprintf("loop [%d, %d)", r.Contract.Hold.LoopStart, r.Contract.Hold.LoopEnd)
		}
		fmt.Printf("effect %s: %d clips, hold %d Hz %s\n", r.Item.Name, len(r.Data.Clips), r.Contract.Hold.SampleRate, hold)
		return nil
	})
	if err != nil {
		cli.Fatal(err)
	}
	order, err := effect.Items()
	if err != nil {
		cli.Fatal(err)
	}
	if err := cli.Pack(source, pack, order); err != nil {
		cli.Fatal(err)
	}
	fmt.Printf("%d effects -> %s\n", count, common.Out)
}
