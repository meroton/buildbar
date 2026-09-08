Development notes for this project
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Project strucutre
=================

This project started off as a set of extended Buildbarn runners and tools
so it followed the Buildbarn's go conventions and styleguide.
As it has grown to also release Rust tools
that are not so tightly coupled to the Buildbarn project
we have created a bit of a frankenstructure.

The main directory is ``//cmd`` directory that contains the programs
we release.
This mixes go and rust code,
each tool uses whichever language is most suitable.

Then there is a plethora of other directories.
We could not find one grand unified structure that allows
everything to be built by both Bazel
and the endemic tools of their respective languages.

* //pkg: go libraries. This is managed by ``gazelle`` to generate BUILD files.
* //lib: rust libraries. The BUILD files are written manually
* //internal: internal go libraries. Currently only for test support/mocking.
* //tools: various tooling.
* //patches: patches to external Bazel modules.

Nomenclature
============

The Buildbarn family of tools has the "bb" prefix
and uses underscores as word dividers.
All other tools use different prefixes and dash notation.

