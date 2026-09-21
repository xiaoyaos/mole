#ifndef MyAppVersion
  #define MyAppVersion "dev"
#endif
#ifndef SourceRoot
  #define SourceRoot "..\.."
#endif

[Setup]
AppId={{8BE86394-E921-4CE4-9A90-0E47FD18B4FC}
AppName=Mole Client
AppVersion={#MyAppVersion}
AppPublisher=xiaoyaos
DefaultDirName={autopf}\Mole
DefaultGroupName=Mole
OutputDir={#SourceRoot}\dist
OutputBaseFilename=mole-client-windows-amd64-setup
Compression=lzma2
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayName=Mole Client

[Files]
Source: "{#SourceRoot}\bin\client\mole-client-windows-amd64.exe"; DestDir: "{app}"; DestName: "mole-client.exe"; Flags: ignoreversion
Source: "{#SourceRoot}\packaging\windows\MoleLauncher.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceRoot}\tap-windows-9.24.7-I601-Win10.exe"; DestDir: "{tmp}"; Flags: deleteafterinstall

[Icons]
Name: "{group}\Mole Client"; Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\MoleLauncher.ps1"""; WorkingDir: "{app}"
Name: "{autodesktop}\Mole Client"; Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\MoleLauncher.ps1"""; WorkingDir: "{app}"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加快捷方式："

[Run]
Filename: "{tmp}\tap-windows-9.24.7-I601-Win10.exe"; Parameters: "/S"; StatusMsg: "正在安装 TAP 虚拟网卡驱动…"; Flags: waituntilterminated runhidden
Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\MoleLauncher.ps1"""; Description: "启动 Mole Client"; Flags: postinstall nowait skipifsilent
