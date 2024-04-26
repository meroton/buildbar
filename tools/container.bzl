load("@rules_oci//oci:defs.bzl", "oci_push")

def container_push_official(name, image, component):
    oci_push(
        name = name,
        image = image,
        repository = "ghcr.io/meroton/buildbar/" + component,
        remote_tags = "@com_github_buildbarn_bb_storage//tools:stamped_tags",
    )
