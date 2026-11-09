#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

[Setup]
AppId={{C2912165-F275-44CC-B4CB-83E853B973F8}
AppName=LumenFolio
AppVersion={#AppVersion}
AppPublisher=Dengdxx
AppPublisherURL=https://github.com/Dengdxx/LumenFolio
DefaultDirName={localappdata}\Programs\LumenFolio
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
MinVersion=10.0
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayName=LumenFolio
UninstallDisplayIcon={app}\LumenFolio.exe
VersionInfoProductTextVersion={#AppVersion}
OutputDir=..\build\release
OutputBaseFilename=LumenFolio-Windows-x64-Setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Files]
Source: "..\LumenFolio.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\LumenFolio"; Filename: "{app}\LumenFolio.exe"; WorkingDir: "{app}"
