PowerPilot is a desktop dashboard and background service for USB UPSes that speak the Megatec/Voltronic protocol (USB ID `0665:5161`). It shows live power, battery and load data, logs power events, sends notifications, and controls the UPS (alarm, self-test, outlet shutdown and restart). It can also show a UPS from a PowerPilot server, from anywhere.

## What's new in v1.2.1

**A redesigned Settings window.**

- **Settings are grouped** into Alerts, Power outage, Battery and logs, Updates and Cloud. Each row has an icon, a short description and its control.
- **Switches** for notifications and the automatic shutdown, like the **UPS Alarm** switch.
- **Compact number fields** with their unit (min, Ah, days).
- **The shutdown delay sits under its switch,** and is greyed out while shutdown is off.
- **Save and Cancel stay at the bottom,** below Updates and Cloud.

### New in v1.2.0

**Tray icon.** PowerPilot now sits in the menu bar (macOS) or the system tray (Windows, Linux), from login.

- **The icon is a battery** filled to the charge, with a bolt on mains power and "!" for a low battery, an overload or a fault. During an outage, macOS shows the charge beside it.
- **macOS:** click the icon for a panel showing:
  - the battery, with the runtime left;
  - load, input, output and battery voltage;
  - a **Mute** button while the alarm sounds;
  - **Summary** and **Open Dashboard**.

  It follows light and dark mode. Right-click for a short menu.
- **Windows and Linux:** a menu with the same reading, each line with an icon. Choose one to open the dashboard. On Windows, a click on the icon opens the dashboard.
- **On macOS, closing the dashboard window leaves PowerPilot in the menu bar.** **Quit** removes the icon until the next login, or until you open the app. The service keeps watching the UPS either way.
- **Linux** needs a tray that supports StatusNotifierItem (KDE, Xfce, Cinnamon and others). On GNOME, install the AppIndicator extension.

**Notifications with a summary.**

- **Every notification ends with the reading,** e.g. "Battery 87.0%, about 22 min left · Load 31.0% (372 W)".
- **During an outage, a summary repeats every 5 minutes**, so you can follow the charge.
- **Show Summary** in the tray posts the reading on request.
- **They follow the *Show notifications* setting**, with USB and in every Cloud mode.

### Earlier

- **v1.1.0:** Cloud: Remote mode shows a UPS from a PowerPilot server; Publish mode shares this computer's UPS with it.
- **v1.0.4:** logs kept on disk, a file per day, with CSV export; choose how long they're kept; Recent Events survives restarts.
- **v1.0.3:** update PowerPilot from Settings; the Linux window always shows the PowerPilot icon; the header shows the version.
- **v1.0.2:** the dashboard window opens at full size on Linux and Windows, and remembers its size.
- **v1.0.1:** on Linux the UPS connects without root (the udev rule is now `70-powerpilot.rules`); Trends tooltips are no longer cut off; permission errors no longer mention the USB cable.

## Upgrading

**From v1.0.3 or later:** open **Settings**, then **Check for updates** and **Update now**. PowerPilot restarts on the new version by itself. The tray icon appears right away on macOS and Windows. On Linux it appears at the next login, or when you open PowerPilot.

**From an earlier version**, upgrade by hand. The background service keeps running the old version until it restarts, so:

**macOS:** Replace **PowerPilot.app** in Applications, then log out and back in. Or restart the service right away from Terminal:

```sh
/Applications/PowerPilot.app/Contents/MacOS/powerpilot install
```

**Windows:** Windows won't replace a running program. In Task Manager, end every **PowerPilot.exe**. Then replace the file and open it.

**Linux:** From the extracted archive, replace the binary. `powerpilot install` also updates the app-menu entry.

```sh
systemctl --user stop powerpilot
cp powerpilot ~/.local/bin/
~/.local/bin/powerpilot install
```

Upgrading from v1.0.0? Also swap the udev rule, then unplug and replug the UPS's USB cable:

```sh
sudo rm /etc/udev/rules.d/99-powerpilot.rules
sudo cp 70-powerpilot.rules /etc/udev/rules.d/
sudo udevadm control --reload && sudo udevadm trigger
```

## Downloads

| System | File |
|---|---|
| macOS 12+ (Apple Silicon and Intel) | `PowerPilot_*_macOS_universal.zip` |
| Windows 10/11 (x64 or ARM) | `PowerPilot_*_windows_amd64.zip` / `_arm64.zip` |
| Linux (x64 or ARM, systemd) | `PowerPilot_*_linux_amd64.tar.gz` / `_arm64.tar.gz` |

`SHA256SUMS.txt` lists checksums for every file.

## Install

**macOS:** Unzip and move **PowerPilot.app** to Applications, then open it. The first launch starts the background service, which then runs at login.

The app isn't notarized, so macOS blocks the first launch. Either right-click the app and choose **Open**, or allow it under *System Settings → Privacy & Security*.

**Windows:** Unzip somewhere permanent and double-click **PowerPilot.exe**. The first launch starts the background service, which then runs at sign-in.

SmartScreen may warn about an unrecognized app. Choose *More info → Run anyway*.

**Linux:** Follow `INSTALL.txt` in the archive. It covers a one-time udev rule (needs `sudo`), then `powerpilot install`.

The dashboard is always at http://127.0.0.1:8484, and the service only listens on this computer.

## Notes

- **Readings:**
  - Battery % uses the same formula as Voltronic's ViewPower software.
  - Runtime, watts and VA are estimates. Set your battery's Ah in Settings for a better runtime estimate.
- **Unreported values:** not every UPS reports every value (temperature, fan speed and power factor are often missing). Anything your UPS doesn't send shows as *Not reported*.
- **Platform testing:** the Windows and Linux builds are new. Please open an issue if your UPS isn't detected there.
