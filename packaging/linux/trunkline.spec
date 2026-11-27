# TrunkLine's .rpm, from a binary packaging/dist.sh already built (no
# compiling here). build-rpm.sh runs rpmbuild with these defined:
#   tl_version  X.Y.Z
#   tl_bin      the trunkline binary
#   tl_here     packaging/linux (the .desktop file, and ../trunkline.svg)
#   tl_license  LICENSE
#
# The shared libraries it needs (GTK 3, WebKitGTK 4.1, glibc) become
# Requires by their sonames, which rpm reads from the binary, so the same
# package works on Fedora and openSUSE, whose package names differ.

Name:           trunkline
Version:        %{tl_version}
Release:        1
Summary:        Desktop client for Subversion (SVN)
License:        GPL-3.0-or-later
URL:            https://github.com/FrancoCoetzer/TrunkLine
Requires:       subversion

# A Go binary built and stripped already: no debug package, no stripping.
%global debug_package %{nil}
%global __strip /bin/true

%description
Browse repositories, check out, see changes with diffs, commit, update,
resolve conflicts, read history, compare branches, and export and import.
Runs the svn command-line client, so it works with the same working copies.
Keeps itself up to date from the project's GitHub releases.

%install
install -D -m 0755 %{tl_bin} %{buildroot}%{_bindir}/trunkline
install -D -m 0644 %{tl_here}/trunkline.desktop %{buildroot}%{_datadir}/applications/trunkline.desktop
install -D -m 0644 %{tl_here}/../trunkline.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/trunkline.svg
install -D -m 0644 %{tl_license} %{buildroot}%{_datadir}/licenses/trunkline/LICENSE

%files
%{_bindir}/trunkline
%{_datadir}/applications/trunkline.desktop
%{_datadir}/icons/hicolor/scalable/apps/trunkline.svg
%{_datadir}/licenses/trunkline/LICENSE
