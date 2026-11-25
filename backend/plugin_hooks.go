package backend

// What the plugin commands tell the parts of the app that run plugins'
// jobs, notifications and the tray.

// rearmSchedule looks at the scheduled jobs again: a plugin switched on
// again may have jobs due.
func (a *App) rearmSchedule() {}

// forgetPluginJobs drops an uninstalled plugin's jobs.
func (a *App) forgetPluginJobs(id string) {}

// rebuildTray gives the tray, while there is one, its menu as it is now.
func (a *App) rebuildTray() {}

// notificationClicked runs a notification's click.
func (a *App) notificationClicked(id string) {}
