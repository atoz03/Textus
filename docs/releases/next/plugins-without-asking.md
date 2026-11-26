### Improved

- Plugins work as soon as they are switched on: Textus no longer asks for each permission when you turn one on, before it uses your AI model, or before it connects to a server over SSH, and a plugin's page no longer lists permissions to revoke. Whatever it lists, a plugin still can't read Textus's own settings or the secrets it keeps.
- A Git plugin commits in a repository with hooks without asking you to trust the repository first; the hooks run as with your own git. A repository whose settings name programs to run is still asked about once.
- Settings lists the Textus AI pages after Editing, Extensions and Shortcuts, and the Plugins page leads with **Install from file…**; making a plugin with Textus AI is in its menu.
