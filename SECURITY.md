# Security policy

## Supported versions

Only the latest release gets security fixes. TrunkLine updates itself, so
staying current is usually automatic. Copies installed for all users from the
.msi are updated by whoever installed them.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through GitHub:
[Report a vulnerability](https://github.com/FrancoCoetzer/TrunkLine/security/advisories/new)
(the Security tab, then "Report a vulnerability").

Include what you found, how to reproduce it, the TrunkLine version
(Help, About) and your OS. You should hear back within a week. Once a fix is
released, the advisory is published and you're credited unless you'd rather
not be.

## What's in scope

Things that are especially worth reporting:

- **Credentials**: a password ending up on disk, in logs, in the process list,
  or anywhere other than svn's stdin and its own credential store.
- **Updates**: anything that gets the app to install a file that doesn't match
  the release's `SHA256SUMS`, or from somewhere other than this repository's
  releases.
- **Certificates**: a server's certificate being trusted without the user
  agreeing to it.
- **Repository content**: a file name, log message, property or diff from a
  repository that can run script in the app's window or run commands.

Problems in svn itself should go to the
[Apache Subversion project](https://subversion.apache.org/security/).
