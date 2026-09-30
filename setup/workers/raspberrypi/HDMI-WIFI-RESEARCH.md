# Pi 5 HDMI and Wi-Fi findings

Hardware tests: 29–30 September 2026. For installation, use the [setup guide](README.md).

**1080p at 60 Hz is the tested display setting for this worker.** At
3440×1440/100 Hz, HDMI repeatedly prevented discovery of the hidden 2.4 GHz
network. Disabling HDMI or selecting 1080p restored discovery. Radio interference
is the leading explanation; its frequency and physical source were not measured.

## Test hardware

| Component | Configuration |
| --- | --- |
| Board | Raspberry Pi 5 Model B Rev 1.0, booting from microSD |
| Monitor | Samsung `LC34G55T`, connected to `HDMI-A-2` |
| OS | Raspberry Pi OS Lite Trixie arm64, 2026-09-15 image |
| Kernel | `6.18.50+rpt-rpi-2712` |
| Wi-Fi | `brcmfmac`, firmware `7.45.265`, NetworkManager `1.52.1` |
| Router | GL.iNet Flint 3, firmware 4.10.1 |
| Network | Hidden SSID, 2.4 GHz channel 1, 40 MHz configured, WPA2/WPA3 mixed |
| Power | `get_throttled=0x0` during checks |

The initial experiment used `wpasupplicant 2:2.10-24+rpt3`, installed during an
earlier WPA3 investigation. Later fresh flashes passed using stock `2:2.10-24`.
All successful connections described here used **WPA2**. WPA3-only operation
has not been verified, and the flasher does not install that beta package.

## Controlled display comparison

The same Pi, monitor, cable, router, channel, and saved credential were used.
NetworkManager temporarily released `wlan0` for three fresh directed scans per
mode. A pass required the network's exact name in the result; an anonymous
beacon alone did not count. Display changes used `kmsblank` and `kmstest`.

| Display state, in test order | Pixel clock | Named SSID found |
| --- | ---: | ---: |
| 3440×1440/100 Hz | 543.500 MHz | 0/3 |
| HDMI disabled | Inactive | 3/3 |
| 3440×1440/100 Hz restored | 543.500 MHz | 0/3 |
| 1920×1080/60 Hz | 148.500 MHz | 3/3 |
| 3440×1440/~60 Hz | 319.750 MHz | 1/3 |

At 1080p, the Pi authenticated, received an IP address, and accepted SSH over
Wi-Fi with Ethernet disconnected. Gateway pings passed 5/5; internet pings
passed 3/3. Lowering the ultrawide refresh rate alone was inconsistent.

An apparently blank console did not disable the HDMI signal: framebuffer
blanking left the display controller active. `kmsblank` disabled the output.
The radio capture was inconclusive; it did not measure interference or establish
whether the Pi deferred transmission or lost replies.

## Fresh flashes and restart checks

1. **First 1080p flash:** stock Wi-Fi software joined the hidden network with HDMI
   connected. SSH, DNS, router access, and internet access passed. Cloud-init
   finished with a recoverable warning about missing `cc_netplan_nm_patch`.
2. **Warning cleanup and failed restart:** removing only the stale module entry
   and rebooting reached the console login prompt, but the Pi was unreachable.
   Its active display timing and logs were not captured. A later headless boot
   connected in about 12 seconds. Because that recovery involved a new boot,
   unplugging HDMI alone was not isolated as the cause of recovery.
3. **Hotplug and reconnection tests:** reconnecting HDMI selected 1080p/60 Hz.
   Native 1080p, explicit 1080p, 720p, and 1080p again each passed 3/3 directed
   scans, Wi-Fi reconnection, and 5/5 gateway pings: **12/12 scans total**.
4. **Two HDMI-connected warm restarts:** Wi-Fi activated at 11.34 and 11.44 seconds.
   Both used 1080p/60 Hz, accepted SSH, and passed 5/5 gateway and 3/3 internet
   pings with Ethernet unavailable. The first used temporary KMS debug logging;
   the second restored the exact original boot arguments. The first boot's logs
   show 1080p selected at 4.29 seconds, before Wi-Fi connected.
5. **Fresh flash with the warning correction:** the user installed another
   freshly flashed card. A new SSH host key and root partition UUID confirmed
   the new installation. Wi-Fi, SSH, and 1080p/60 Hz worked; internet pings passed
   3/3. Cloud-init reported `done`, no errors or recoverable warnings, and no
   failed services were listed.

Temporary diagnostic helpers and logging overrides were removed. No custom
boot service, router change, or new Wi-Fi package was needed for the follow-up.
The worker remains on the setup's standard 1080p configuration.

**The earlier failed restart remains unexplained.** The follow-up did not
reproduce it. These short tests cover one hardware combination; they do not
establish long-term stability, repeated cold-boot reliability, or compatibility
with every monitor and cable.

## What setup configures

For the 1080p choice, setup adds these arguments to the existing single line
in the image's `cmdline.txt`, preserving its root filesystem and other settings:

```text
video=HDMI-A-1:1920x1080@60 video=HDMI-A-2:1920x1080@60
```

The arguments select the tested 148.500 MHz mode. They omit `M`, which requests
new CVT timings, and omit force-enable suffixes so disconnected ports stay off.
The official compressed download is preserved; only a temporary copy is edited
before Imager writes and verifies the media. See
[Linux mode syntax](https://docs.kernel.org/fb/modedb.html) and
[Raspberry Pi display settings](https://www.raspberrypi.com/documentation/computers/configuration.html#configure-display-settings).

The cloud-init correction supplies the pinned image's final module list with
only `netplan_nm_patch` omitted. It preserves all working modules and their order.
The missing reference is documented in the
[Trixie issue tracker](https://github.com/raspberrypi/trixie-feedback/issues/45).
Recheck this override whenever the pinned image changes.

## Supporting research

- An [original Pi 4 investigation](https://www.enricozini.org/blog/2019/himblick/raspberry-pi-4-loses-wifi-at-2560x1440-screen-resolution/)
  reproduced resolution-dependent Wi-Fi failures across several boards. It is
  precedent for interference, not proof of the exact mechanism on this Pi 5.
- The [upstream HDMI patch](https://marc.info/?l=devicetree&m=160397883020692&w=4)
  describes display/Wi-Fi cross-talk. The
  [pinned driver implementation](https://github.com/raspberrypi/linux/blob/648ac9c948c43a95de21fa9b171689ba9a4e1fd1/drivers/gpu/drm/vc4/vc4_hdmi.c#L1688-L1735)
  applies a narrow timing workaround when `wifi-2.4ghz-coexistence` is present.
  Our failing clocks fall outside its trigger range, and the property was absent
  on this Pi. Adding it is not a demonstrated fix here.
- A [Pi 5 report](https://forums.raspberrypi.com/viewtopic.php?t=371398)
  describes a similar display interaction on 5 GHz. Changing bands, cables,
  ports, or channels therefore needs its own test.
- [NetworkManager documents hidden SSID discovery](https://www.networkmanager.dev/docs/api/latest/settings-802-11-wireless.html).
  A visible anonymous beacon does not prove that discovery of the name works.
- Raspberry Pi documents [legacy HDMI options](https://www.raspberrypi.com/documentation/computers/legacy_config_txt.html)
  as unsupported on current KMS installations. Use native `video=` arguments.

Local evidence is retained under `~/.cache/vm-control-plane/` in
`wpa3-recovery/pi-worker-8gb-1/`, `hdmi-followup/`, and
`raspberrypi/fresh-boot-check-*/`. These contain the scan matrix, connectivity
results, and boot logs used above.
