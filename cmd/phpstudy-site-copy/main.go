//go:build windows

// Command phpstudy-site-copy prepares a reviewable site plan without changing
// phpStudy or the PHPTools runtime. Its live checks use a disposable runtime.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
)

func main() {
	root := flag.String("root", `E:\PHPTools`, "read-only destination metadata and package source")
	inventory := flag.String("inventory", `build\migration\inventory\sites-source.json`, "source inventory")
	phpMap := flag.String("php-map", `build\migration\php-map.json`, "prepared PHP instance map")
	output := flag.String("output", `build\migration\sites-import-plan.json`, "stable-ID site import plan")
	report := flag.String("report", `build\qa\phpstudy-sites-acceptance.json`, "isolated validation report")
	check := flag.Bool("check", true, "validate all sites and PATH_INFO in a disposable runtime")
	apply := flag.Bool("apply", false, "apply a validated plan to stopped PHPTools; never starts or stops services")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, *root, *inventory, *phpMap, *output, *report, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *apply {
		if err := applyTarget(ctx, *root, *output, *report); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func run(ctx context.Context, root, inventory, phpMap, output, report string, check bool) error {
	plan, existing, instances, packages, err := preparePlan(root, inventory, phpMap, output)
	if err != nil {
		return err
	}
	fmt.Printf("Prepared %d sites; preserved %d existing sites; plan: %s\n", len(plan), len(existing), output)
	if check {
		return validatePlan(ctx, root, report, plan, existing, instances, packages)
	}
	return nil
}
