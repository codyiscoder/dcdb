# [dcdb](https://dcdb.developercody.net/)

A tiny data scripting language. Encrypted `.dcdb` files, a built-in query console,
and a handful of commands that read, shape, join, and save rows. Standard library
only, no dependencies.

```
$ go build -o dcdb ./src
$ ./dcdb -p my-script.dc      # run a script, dump the final table
$ ./dcdb -k vault-key x.dc    # decrypt with a key
$ ./dcdb                      # built-in query console
```

## Options

| flag | meaning |
| --- | --- |
| `-p, --print` | enable `print`/`printvar`, dump the final table |
| `-t, --time` | print total execution time |
| `-k, --key KEY` | database encryption key (default `""`) |
| `-dp, --data-path P` | create an empty database file at P if missing |
| `-op, --output-path P` | make sure the parent folder of P exists |
| `-v, --version` | show version |
| `-h, --help` | show help |

## Language

Comments run to end of line with `//`. Values are auto-typed: ints, floats,
bools, strings, null (a missing field reads as null). Operators: `> >= < <= == !=`.
Ordering comparisons require numbers or null; anything else is a runtime error.

### rows

| command | does |
| --- | --- |
| `load <name>` | read `data/<name>.dcdb` (or the path as given) |
| `use <path>` | navigate; arrays become rows, a map/leaf becomes one `value` row |
| `only <f> <op> <v>` | keep matching rows |
| `delete <f> <op> <v>` | drop matching rows |
| `sort <f> [asc\|desc]` | order rows (default desc) |
| `take <n>` | keep the first n rows |
| `add <f>=<v> ...` | append a row |
| `update <f> <v> <wf> <wv>` | set field where the other field matches |
| `save <name>` | write `data/<name>.dcdb` (or path), encrypted |
| `show [f...]` | keep only the listed columns |
| `drop <f> ...` | remove columns |
| `rename <old> <new>` | rename a column |
| `unique <f>` | keep the first row per value |
| `reverse` | reverse row order |
| `clear` | empty the table |
| `join <name> [left] on <lf> <rf>` | hash-join the table with `<name>`; `left` keeps unmatched rows; collisions merged left-first |
| `index <f>` / `index drop [f]` | build/drop a lookup index on a field |

### flow, variables, misc

| command | does |
| --- | --- |
| `print`, `printvar <v>` | dump the table; print one variable |
| `set <v> <val>` | assign a variable (auto-typed) |
| `echo <text>` | print text |
| `hash <text>` | print the sha256 hex digest |
| `while <v> <op> <v2>` ... `end` | loop while the condition holds |
| `for <v> <from> <to> [step]` ... `end` | counted loop |
| `if <v> <op> <v2>` ... [`else`] ... `end` | conditional |
| `proc <name>` ... `end`, `call <name>` | reusable body (redefinition replaces) |
| `include <file>` | run another `.dc` file (relative to the script dir, depth-capped) |
| `++ <v>`, `-- <v>` | increment / decrement |
| `+ <v> <n>`, `- <v> <n>`, `* <v> <n>`, `/ <v> <n>` | arithmetic in place |
| `key <pass>` | set the encryption key |
| `sleep <ms>`, `exit` | pause; stop the script (`run` is reserved) |

### transactions

| command | does |
| --- | --- |
| `begin` | snapshot the table, variables, and current document |
| `commit` | keep the changes |
| `rollback` | restore the snapshot; a runtime error inside a transaction also rolls back to `begin` |

## Storage

`.dcdb` files are a tiny binary format, not JSON:

```
DCDB | 03 | salt(16) | iv(12) | aes-256-gcm payload + tag(16)
```

* key: `PBKDF2-HMAC-SHA256(passphrase, salt, 65536 iters, 32 bytes)`
* cipher: AES-256-GCM (authenticated — wrong key or tampering fails loudly)
* random salt and iv per write; atomic write via tmp file + rename
* v1 (plaintext) and v2 (AES-128-CTR + crc32) files still load
* `load`/`save` default to `data/`; pass a full path like `output/foo.dcdb` to go elsewhere

## A tiny example

Save this as `scores.dc` and run it:

```
$ ./dcdb -p scores.dc
```

```dc
add name="piña" score=9
add name="lei"   score=7
only score >= 8
sort score desc
save output/top.dcdb
```

`-p` prints the final table; `output/top.dcdb` lands on disk, folders created for
you. Plain names like `load users` / `save users` go through `data/`.
The docs live in `index.html` (single file, no frameworks).

## Tests

```
$ go test ./...     # 30 tests: codec, keyed io, scripting, parallel, new commands
$ go vet ./...
```
