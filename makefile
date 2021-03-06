# Makefile for building utility packages that are used by Safe Harbor.

PRODUCTNAME=Docker Utilities
ORG=Scaled Markets
PACKAGENAME=docker

PROJECTROOT := $(shell pwd)
UTILITIESDIR:=$(realpath $(PROJECTROOT)/../utilities)
RESTDIR := $(realpath $(PROJECTROOT)/../rest)

.DELETE_ON_ERROR:
.ONESHELL:
.SUFFIXES:
.DEFAULT_GOAL: all

SHELL = /bin/bash

CURDIR=$(shell pwd)
CPU_ARCH:=$(shell uname -s | tr '[:upper:]' '[:lower:]')_amd64

.PHONY: all compile clean info
.DEFAULT: all

src_dir = $(CURDIR)/src
pkg_dir = $(CURDIR)/pkg
build_dir = $(CURDIR)/bin

all: compile

$(build_dir):
	mkdir $(build_dir)

compile: $(build_dir) $(src_dir)/$(PACKAGENAME)/*.go
	@echo "CPU_ARCH=${CPU_ARCH}"
	GOPATH=$(CURDIR):$(UTILITIESDIR):$(RESTDIR) go install $(PACKAGENAME)

$(pkg_dir)/$(CPU_ARCH)/$(PACKAGENAME)/*.a : compile

$(build_dir)/$(PACKAGENAME): compile

clean:
	rm -r -f $(build_dir)/*
	rm -r -f $(pkg_dir)/*
