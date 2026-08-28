"""Thin wrappers around third-party rust_* rules that bake in this repo's
own defaults, so they live in one place instead of being repeated at every
call site. Only a linux_amd64 rust toolchain is registered so far (see
//MODULE.bazel) — cross-compiling for other CI matrix platforms is a
deferred, separate step.
"""

load(
    "@rules_rust//rust:defs.bzl",
    _rust_binary = "rust_binary",
    _rust_doc_test = "rust_doc_test",
    _rust_library = "rust_library",
)

_LINUX_X86_64 = [
    "@platforms//os:linux",
    "@platforms//cpu:x86_64",
]

def rust_library(**kwargs):
    _rust_library(target_compatible_with = _LINUX_X86_64, **kwargs)

def rust_binary(**kwargs):
    _rust_binary(target_compatible_with = _LINUX_X86_64, **kwargs)

def rust_doc_test(**kwargs):
    _rust_doc_test(target_compatible_with = _LINUX_X86_64, **kwargs)
