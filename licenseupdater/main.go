// Copyright 2026 Redpanda Data, Inc.
//
// Licensed as a Redpanda Enterprise file under the Redpanda Community
// License (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
// https://github.com/redpanda-data/redpanda/blob/master/licenses/rcl.md

package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/template"
	"time"

	"golang.org/x/sync/errgroup"
)

type templateData struct {
	Organization string
	Year         int
}

var (
	embeddedLicenses = []string{"mit", "bsl", "rcl", "apache"}
	//go:embed templates/*
	licenses embed.FS

	shortLicenseHeaderTemplates = map[string]*template.Template{}
	licenseHeaderTemplates      = map[string]*template.Template{}
	licenseTemplates            = map[string]*template.Template{}

	licenseTemplateData = &templateData{
		Organization: defaultOrganization,
		Year:         time.Now().Year(),
	}

	writer *fsWriter
)

func init() {
	for _, embedded := range embeddedLicenses {
		shortHeader := template.Must(template.New("").ParseFS(licenses, "*/"+shortHeaderName(embedded)))
		header := template.Must(template.New("").ParseFS(licenses, "*/"+headerName(embedded)))
		full := template.Must(template.New("").ParseFS(licenses, "*/"+fullName(embedded)))

		shortLicenseHeaderTemplates[embedded] = shortHeader
		licenseHeaderTemplates[embedded] = header
		licenseTemplates[embedded] = full
	}
}

func dieOnError(err error) {
	if err != nil {
		log.Printf("error processing files: %v", err)
		os.Exit(1)
	}
}

func main() {
	var configFile string
	var check bool
	flag.StringVar(&configFile, "config", ".licenseupdater.yaml", "path to config file")
	flag.BoolVar(&check, "check", false, "check diffs and exit")

	flag.Parse()

	writer = &fsWriter{write: !check}
	if check {
		writer.differ = diffChecker()
	}

	config, err := loadConfig(configFile)
	dieOnError(err)
	dieOnError(doMain(config))

	if check {
		if err := writer.differ.error(); err != nil {
			fmt.Print(err.Error())
			os.Exit(1)
		}
	}
}

func doMain(config *config) error {
	var group errgroup.Group

	ch := make(chan *matchedFile, 1000)
	group.Go(func() error {
		return config.walk(ch)
	})

	for f := range ch {
		group.Go(f.process)
	}

	if err := group.Wait(); err != nil {
		return err
	}

	if err := config.writeTopLevelLicense(); err != nil {
		return err
	}

	if err := config.writeLicenses(); err != nil {
		return err
	}

	return config.writeStaticFiles()
}

func shortHeaderName(name string) string {
	name = strings.ToUpper(name)
	return fmt.Sprintf("%s.short", name)
}

func headerName(name string) string {
	name = strings.ToUpper(name)
	return fmt.Sprintf("%s.header.tmpl", name)
}

func fullName(name string) string {
	name = strings.ToUpper(name)
	return fmt.Sprintf("%s.md.tmpl", name)
}
