Lightweight content-addressable build avoidance for your pipeline
=================================================================

``re-memoize`` wraps any command execution
with a content addressable caching layer using the `remote execution api`_.
You decide the command and what external state should be the cache key.
``re-memoize`` then runs it if it has not already been executed
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

Usage
-----

``re-memoize`` runs a command and caches its result in a REAPI action cache.
This is a quick and efficient way to cache, memoize, its execution
So subsequent callers of the same program can quickly download the resulting files and logs.

::

    re-memoize run \
        --directory-digest "$lookup_key" \
        --remote "$remote" \
        -- slow-running-process args...

The lookup key to cache execution is a REAPI ``directory``_.
This can be computed with the ``digest`` subcommand.

::

    # An entire directory
    lookup_key="$( \
        re-memoize digest \
        ./input_tarball/
    )"

    # Parts of a directory with filters
    lookup_key="$( \
        re-memoize digest \
        ./input_tarball/
    )"


The command itself is cached as well
++++++++++++++++++++++++++++++++++++

We can reuse the same key for different commands

::

    key="$(re-memoize digest /dev/null --root /)"

    # The first run takes ten seconds.
    $ time re-memoize run --remote grpc://localhost:8980 --directory-digest "$key" sh -c 'sleep 10; echo hello'
    hello
    0.00user 0.00system 0:10.01elapsed 0%CPU (0avgtext+0avgdata 11072maxresident)k
    0inputs+0outputs (0major+984minor)pagefaults 0swaps

    # The next is almost instantaneous.
    $ time re-memoize run --remote grpc://localhost:8980 --directory-digest "$key" sh -c 'sleep 10; echo hello'
    re-memoize: cache hit (e181d37528e0939cc81cf2b268a720290a205d47e09940f08053383de987f4b1/140)
    hello
    0.00user 0.00system 0:00.00elapsed 66%CPU (0avgtext+0avgdata 10816maxresident)k
    0inputs+0outputs (0major+468minor)pagefaults 0swaps

    # But if we switch out the print it takes ten seconds again.
    time bb-memoize run --remote grpc://localhost:8980 --directory-digest "$key" sh -c 'sleep 10; echo hello hello'
    hello hello
    0.00user 0.00system 0:10.01elapsed 0%CPU (0avgtext+0avgdata 11008maxresident)k
    0inputs+0outputs (0major+984minor)pagefaults 0swaps

Different roots in the directory message
++++++++++++++++++++++++++++++++++++++++

As the path in the directory is very important
you may want to change it with the ``--root`` flag
The following three invocations computes the digest
for different directory structures for the same file

::

    $ re-memoize digest cmd/re_memoize/README.rst
    973d09a8c49d21d2fdc3ef7f538c87f894c852fddd582d1ac2993dc63038f9fb/77
    # Equivalent to cd-ing one directory deeper.
    $ re-memoize digest --root cmd cmd/re_memoize/README.rst
    aaba56e3be1860456e4808bb795dc7990dfcbb04cf444dcf391f8de3b3119ba4/84
    $ re-memoize digest --root cmd/re_memoize cmd/re_memoize/README.rst
    19b5d13d0f5567ef720f455790e337e0a771d6b0e7b82940ed3de3a813c62a90/85

This makes it possible to run from everywhere
and not use the current working directory as an implicit input in the computation.
However, it is important to be consistent in how you digest your inputs
otherwise the cache lookup will not use the same key.

Action key, the digest
----------------------

``re-memoize run`` caches results based on a key
that encodes the command and an (input root) directory.
Through the ``--directory-digest`` flag you can provide the directory part
and the command is simply the command arguments given as positional arguments.
Neither environment nor external state or file access information for the command itself is used,
as this is not meant to be a hermetic cache.

To help in creating the directory digest you can run ``re-memoize digest``
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

re-memoize has not yet implemented support for authenticated access to the RBE.

Uploading the CAS-indexed files
--------------------------------

Not technically required,
but useful later if you want to investigate what happened --
so it's recommended.
If the files were already built by Bazel (with RBE),
they're already present in CAS,
and upload short-circuits via find_missing_blobs.
