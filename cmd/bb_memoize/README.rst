Lightweight content-addressable build avoidance for your pipeline
=================================================================

``bb-memoize`` wraps any command execution
with a content addressable caching layer using the `remote execution api`_.
You decide the command and what external state should be the cache key.
``bb-memoize`` then runs it if it has not already been executed
and creates a cache entry with the output files and logs.
If someone else has already run the command
you of course get their results.

This can be seen as a lightweight complement to a build system like Bazel
where truly external commands can still use some caching.
Where a full Bazelification is either not possible
or too expensive.
Unlike Bazel however the caller of the command must make sure to setup a good cache key
we do not sandbox the execution or track external state
but it can quickly be deployed for any command.

The command itself and its inputs aren't hashed by this tool.
So if you tweak a command (e.g. via your shell's up-arrow)
without changing anything else,
it's still keyed the same as the previous run
and will likely be treated as a cache hit -- a false positive.

The shell command you run
can pull in arbitrary input from external sources,
so there's no way to compute a reliable cache key for it.
If you do want full control,
you must use a proper build system like Bazel.

.. _remote execution api: https://github.com/bazelbuild/remote-apis

Action key, the digest
----------------------

``bb-memoize run`` caches results based on a key
that encodes the command and an (input root) directory.
Through the ``--directory-digest`` flag you can provide the directory part
and the command is simply the command arguments given as positional arguments.
Neither environment nor external state or file access information for the command itself is used,
as this is not meant to be a hermetic cache.

To help in creating the directory digest you can run ``bb-memoize digest``
and point it to a file or directory (or files within a directory).
See its help for more information about how to index only parts of directory structures.
A common use-case is to have hardware testing of big flash images
where most commits touch some part of the tree but we want to avoid running an expensive test
where only certain parts of the image are relevant,
those are the bits we want to compute the key for.

Think of it as the distinction between "target under test"
and "test support code", where they are built into one large tarball.
A regular Bazel rule would rerun this with every change.

Digesting external state
------------------------

You may also want to have information about the hardware test server version::

    curl hardware-test.internal.example.com/version >> input_tree/server_version.txt

Then just compute the digest for the input tree as usual.
Now any changes to the server version will cause a cache miss.

We don't want git patch introspection
-------------------------------------

Many CI systems would address this by looking at the git commit
that is tested and have a ruleset for which files are important or not.
But this has a couple of drawbacks that are better solved by using the content hash of the files.

Multiple Change Requests with the same change would still trigger the test independently.
A Change that is quickly reverted back to the main branch would trigger two tests,
whereas the revert is completely unnecessary.
And by avoiding the git link entirely
we get the same behavior for a developer regardless of how she found the code
it could be a tarball of the source tree rather than requiring the git history to avoid tests.
If you want to encode more information into the key you can create files with that information and place them in the directory.

Configuration
-------------

To switch the RBE environment use the
``--remote`` and ``--instance-name`` flags.
You can also configure the maximum message size with
``--max-message-size``.

Instance Name
+++++++++++++

RBE Servers like Buildbarn can be configured to have a special instance name for this cache,
as it will be very small you may want to handle these keys separately
to have a longer retention or other functionality.

Authentication
++++++++++++++

bb-memoize has not yet implemented support for authenticated access to the RBE.

Uploading the CAS-indexed files
--------------------------------

Not technically required,
but useful later if you want to investigate what happened --
so it's recommended.
If the files were already built by Bazel (with RBE),
they're already present in CAS,
and upload short-circuits via find_missing_blobs.
