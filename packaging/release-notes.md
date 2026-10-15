PowerPilot is a desktop dashboard and background service for USB UPSes that speak the Megatec/Voltronic protocol (USB ID `0665:5161`). It shows live power, battery and load data, logs power events, sends notifications, and controls the UPS (alarm, self-test, outlet shutdown and restart).

## What's new in v1.0.2

- **The dashboard window opens at full size on Linux and Windows.** It used to open inside your already-running Chrome or Edge, which ignored the requested size. So it came up too narrow for the dashboard, and on KDE with a generic icon. It now runs as a window of its own, with the PowerPilot icon.
- **The window remembers its size.** Resize it and the next launch opens at that size. Closing it while maximized keeps the last normal size.

### From v1.0.1

- **Linux: the UPS connects without root.** The v1.0.0 udev rule ran too late for systemd to give your user access, so the dashboard showed *no permission to open /dev/hidrawN*. The rule is now `70-powerpilot.rules`.
- **Trends tooltips** are no longer cut off at the top when the dashboard fills the window.
- **Permission errors** no longer tell you to check the USB cable.

## Upgrading

The background service keeps running the old version until it restarts, so:

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
