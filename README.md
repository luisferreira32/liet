# Life is Expensive - Terminal based application

Did you ever think: I need an application to manage my finances? Then look no more! Microsoft Excel is the perfect tool for you! However, if you want a terminal based application to track how expensive your life is without anything too fancy, give *liet* a chance.

## Installation

The tool itself is a standalone binary. If you have Go installed you can use:
```bash
go install github.com/luisferreira32/liet
```

Or download directly from the releases: https://github.com/luisferreira32/liet/releases


## Usage

The first rule of *liet* is that you're one *l* away from f*in it up. So just do everyone a favor and use an alias:
```bash
alias l=liet
```

The normal workflow would be to input your expenses, e.g.:
```bash
l 42.6 groceries
l 20.12 dog -c "he was so dirty he needed to go into the car wash"
l 1 misc
```

And you can observe some statistics if requested, e.g.:
```bash
l -w # short for: what am I doing with my life
```

There are also a couple environment variables that can configure default behaviors:
- `LIET_CONFIG` points towards a configuration file
- `LIET_LOG_LEVEL` indicates which level of logging you desire in the application
- `LIET_LOG_FILE` the location where logs will be dumped
- `LIET_DEBUG` activates the debug mode and pipes all logs to stderr

The configuration file mentioned supports the following keys
- `database=/my/path/foobar.db` where the path specified is to an sqlite3 database

## Uninstall

If you're ever done with this you only have to remove one binary and it is no longer "installed". However, you might want to remove any leftover files. You can go through the `yeet` process with:
```
l -yeet
```

## Contributions & Issues

### Bugs
The issue must be prefixed with [BUG] and some descriptive title, e.g.:

	[BUG] database coult not be wiped

The issue description must contain the following segments:

[ERROR] [any error log that can be obtained]

[DEBUG] build info obtained with go version -m liet

### Features
An issue must be opened and must be prefixed with [FEATURE]. An accompanying pull request might be opened referecing the feature request.

A reasonable description of the feature request must exist within the feature issue. Before opening one take into consideration that:
1. There might be a similar [FEATURE] request - join the discussion there to shape the future of the tool;
1. This is a minimalistic tool and a minimalistic repo structure;
1. The tool must be platform agnostic;
1. Life is expensive `¯\_(ツ)_/¯`

## Road map
- [ ] support for windows binary on the automated releases #2
- [ ] automate PR checks, PR/issue triage, and other processes #3
- [ ] create static webpage for downloads #4