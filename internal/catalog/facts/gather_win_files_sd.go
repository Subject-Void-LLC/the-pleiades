// Package facts: what the Windows files script reads through Win32 rather
// than .NET (owner decisions 2026-11-19): a file's security descriptor from
// the handle it hashes (its DACL, mandatory label and scoped policy, which
// Get-Acl does not return), and its alternate data streams, which .NET
// Framework will not open by name.
package facts

// windowsFileSDHelper is C# the files script compiles with Add-Type, kept to
// the C# 5 that Windows PowerShell 5.1 compiles.
//
// Sddl reads, through GetSecurityInfo on the open handle, the DACL, the
// mandatory label and the scoped policy in one descriptor, as SDDL; nil when
// the handle refuses one, which the script reads as private. Reading it from
// the handle the digest is read from means the descriptor judged and the
// bytes hashed belong to one file, whatever renames the path meanwhile.
//
// Streams lists a path's named streams (FindFirstStreamW/FindNextStreamW, at
// most maxStreams) and digests them: one SHA-256 over, for each stream in
// ordinal order of its name, the name, a NUL, the stream's own SHA-256 (or,
// for one past perStream or past total bytes read, its length, counted as
// unread, so a large stream costs nothing and still shows a change of size),
// and a NUL. Each stream is opened by name through CreateFileW. It returns
// state (listed or unreadable), the names without their ":" and ":$DATA",
// the digest, how many went unread, and whether the listing stopped at
// maxStreams.
const windowsFileSDHelper = `
using System;
using System.IO;
using System.Text;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using Microsoft.Win32.SafeHandles;
public static class PleiadesFileSD {
  [DllImport("advapi32.dll", SetLastError = true)]
  static extern uint GetSecurityInfo(SafeHandle h, int type, int info, IntPtr o, IntPtr g, IntPtr d, IntPtr s, out IntPtr sd);
  [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  static extern bool ConvertSecurityDescriptorToStringSecurityDescriptorW(IntPtr sd, int rev, int info, out IntPtr str, out int len);
  [DllImport("kernel32.dll")]
  static extern IntPtr LocalFree(IntPtr p);
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
  struct StreamData { public long Size; [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 296)] public string Name; }
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern IntPtr FindFirstStreamW(string path, int level, out StreamData data, int flags);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern bool FindNextStreamW(IntPtr h, out StreamData data);
  [DllImport("kernel32.dll")]
  static extern bool FindClose(IntPtr h);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
  static extern SafeFileHandle CreateFileW(string name, uint access, uint share, IntPtr sa, uint disposition, uint flags, IntPtr template);

  public static string Sddl(SafeHandle h) {
    const int info = 0x4 | 0x10 | 0x40;
    IntPtr sd;
    if (GetSecurityInfo(h, 1, info, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, IntPtr.Zero, out sd) != 0) { return null; }
    try {
      IntPtr s; int n;
      if (!ConvertSecurityDescriptorToStringSecurityDescriptorW(sd, 1, info, out s, out n)) { return null; }
      try { return Marshal.PtrToStringUni(s); } finally { LocalFree(s); }
    } finally { LocalFree(sd); }
  }

  public static object[] Streams(string path, long perStream, long total, int maxStreams) {
    StreamData d;
    IntPtr h = FindFirstStreamW(path, 0, out d, 0);
    var found = new List<KeyValuePair<string, long>>();
    bool truncated = false;
    if (h == new IntPtr(-1)) {
      if (Marshal.GetLastWin32Error() != 38) { return new object[] { "unreadable", new string[0], "", 0, false }; }
    } else {
      try {
        do {
          if (d.Name != "::$DATA") {
            if (found.Count >= maxStreams) { truncated = true; break; }
            found.Add(new KeyValuePair<string, long>(d.Name, d.Size));
          }
        } while (FindNextStreamW(h, out d));
      } finally { FindClose(h); }
    }
    found.Sort(delegate(KeyValuePair<string, long> a, KeyValuePair<string, long> b) { return string.CompareOrdinal(a.Key, b.Key); });
    var all = new MemoryStream();
    var names = new List<string>();
    long used = 0; int unread = 0;
    foreach (var s in found) {
      string shown = s.Key;
      if (shown.StartsWith(":")) { shown = shown.Substring(1); }
      if (shown.EndsWith(":$DATA")) { shown = shown.Substring(0, shown.Length - 6); }
      names.Add(shown);
      byte[] nb = Encoding.UTF8.GetBytes(s.Key);
      all.Write(nb, 0, nb.Length); all.WriteByte(0);
      byte[] part = null;
      if (s.Value <= perStream && used + s.Value <= total) {
        using (var fh = CreateFileW(path + s.Key, 0x80000000, 7, IntPtr.Zero, 3, 0x02000000, IntPtr.Zero)) {
          if (!fh.IsInvalid) {
            using (var fs = new FileStream(fh, FileAccess.Read)) { part = SHA256.Create().ComputeHash(fs); }
            used += s.Value;
          }
        }
      }
      if (part == null) { unread++; part = Encoding.UTF8.GetBytes("unread:" + s.Value); }
      all.Write(part, 0, part.Length); all.WriteByte(0);
    }
    byte[] sum = SHA256.Create().ComputeHash(all.ToArray());
    var hex = new StringBuilder();
    foreach (byte b in sum) { hex.Append(b.ToString("x2")); }
    return new object[] { "listed", names.ToArray(), hex.ToString(), unread, truncated };
  }
}
`

// windowsFileFunctions are the files script's PowerShell functions over that
// helper. Test-WorldReadable judges an SDDL: readable by everyone when its DACL
// is null (it grants everyone everything), or when an entry for Everyone,
// Authenticated Users or Users allows read and no entry denies read to anyone
// (owner decision 2026-11-19); private when the SDDL could not be read, when
// any entry is a callback (a conditional entry) or of a kind this does not
// know, or when its system list holds anything, which, asked only for the
// label and the scoped policy, is a mandatory label or a central access
// policy. Entries that only children inherit (InheritOnly, 0x08, compared as
// an integer: Windows PowerShell 5.1 throws "Specified cast is not valid" on
// -band over the byte-backed AceFlags enum, measured on 5.1.26100) are not the
// file's own. Read is
// FILE_READ_DATA or GENERIC_READ or GENERIC_ALL. Test-LinkedAbove says whether
// a directory above the path is a reparse point (a junction, a symbolic link,
// a mounted volume): the same rule as a POSIX path through a linked directory.
const windowsFileFunctions = `
function Test-WorldReadable([string]$sddl) {
  if (-not $sddl) { return $false }
  $sd = New-Object Security.AccessControl.RawSecurityDescriptor($sddl)
  if ($null -ne $sd.SystemAcl -and $sd.SystemAcl.Count -gt 0) { return $false }
  if ($null -eq $sd.DiscretionaryAcl) { return $true }
  $allow = $false
  foreach ($a in $sd.DiscretionaryAcl) {
    if ($a -isnot [Security.AccessControl.CommonAce] -or $a.IsCallback) { return $false }
    if (([int]$a.AceFlags -band 0x08) -ne 0) { continue }
    if (($a.AccessMask -band 0x90000001) -eq 0) { continue }
    if ([string]$a.AceQualifier -eq 'AccessDenied') { return $false }
    if (@('S-1-1-0', 'S-1-5-11', 'S-1-5-32-545') -contains $a.SecurityIdentifier.Value) { $allow = $true }
  }
  return $allow
}
function Test-LinkedAbove([string]$p) {
  $q = $p
  while ($true) {
    $q = [IO.Path]::GetDirectoryName($q)
    if (-not $q) { return $false }
    try { if (([IO.File]::GetAttributes($q) -band [IO.FileAttributes]::ReparsePoint) -ne 0) { return $true } } catch { return $true }
  }
}
`
