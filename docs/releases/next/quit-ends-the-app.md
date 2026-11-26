### Fixed

- Quitting Textus ends it. **Quit** in the macOS Dock or app switcher no longer closes the windows and leaves Textus running, and the tray icon goes with the app instead of staying in the Windows notification area.
- Closing the last window with **Keep running in the background** off ends Textus, also while a plugin's background job or floating view is open.
- When Textus quits, a window with unsaved changes comes to the front with its question instead of holding up the quit unseen.
- `textus commands run quit` quits without opening a window first.
