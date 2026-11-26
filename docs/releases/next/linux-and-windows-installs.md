### Platform notes

- **macOS:** Textus now requires macOS 12 or later; 1.4.6 is the last version for macOS 11.
- **Linux:** the AppImage is replaced by an archive that `install.sh` installs in your home folder, where Textus updates itself (`curl -fsSL https://github.com/CatVinci-Studio/Textus/releases/latest/download/install.sh | sh`); the `.deb` and `.rpm` install it in `/opt/textus`, and for those you download each new version yourself.
- **Linux:** test builds come as an `.rpm` too.
- **Windows:** the portable zip is no longer built; install Textus with the installer.
