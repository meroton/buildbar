load("@rules_oci//oci:defs.bzl", "oci_push")

def container_push_official(name, image, component):
    oci_push(
        name = name,
        image = image,
        repository = "ghcr.io/meroton/buildbar/" + component,
        remote_tags = "@com_github_buildbarn_bb_storage//tools:stamped_tags",
    )

def container_push_meroton(name, image, component):
    oci_push(
        name = name,
        image = image,
        repository = "273354657929.dkr.ecr.eu-central-1.amazonaws.com/meroton/buildbarn/" + component,
        remote_tags = "@com_github_buildbarn_bb_storage//tools:stamped_tags",
    )
