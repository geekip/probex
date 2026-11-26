export type FileKind =
  'text' | 'image' | 'audio' | 'video' | 'pdf' | 'download';
export function fileKind(name: string): FileKind {
  const extension = name.toLowerCase().split('.').at(-1)!;
  if (
    ['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'bmp', 'ico', 'svg'].includes(
      extension,
    )
  )
    return 'image';
  if (
    ['mp3', 'wav', 'ogg', 'oga', 'opus', 'flac', 'aac', 'm4a'].includes(
      extension,
    )
  )
    return 'audio';
  if (['mp4', 'm4v', 'webm', 'ogv', 'mov'].includes(extension)) return 'video';
  if (extension === 'pdf') return 'pdf';
  if (
    [
      'bin',
      'exe',
      'dll',
      'so',
      'dylib',
      'zip',
      'gz',
      'bz2',
      'xz',
      '7z',
      'rar',
      'tar',
      'db',
      'sqlite',
      'sqlite3',
      'woff',
      'woff2',
      'ttf',
      'otf',
    ].includes(extension)
  )
    return 'download';
  return 'text';
}
