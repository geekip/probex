import { posix, win32 } from 'node:path';
import type WebSocket from 'ws';
import type { Node } from '../src/api';
import { encode } from './data';

export function terminal(socket: WebSocket, node: Node, release: () => void) {
  const windows = node.info.os === 'windows';
  let cwd = windows ? 'C:\\Users\\Administrator' : '/root';
  let line = '',
    finished = false,
    program = '';
  let timer: ReturnType<typeof setInterval> | undefined;
  let cols = 80,
    rows = 24,
    historyIndex = 0;
  const history: string[] = [],
    environment: Record<string, string> = {};
  const prompt = () =>
    windows
      ? `PS ${cwd}> `
      : `\x1b[32mroot@${node.name}\x1b[0m:\x1b[34m${cwd}\x1b[0m# `;
  function write(text: string) {
    if (socket.readyState === 1)
      socket.send(JSON.stringify({ type: 'term_output', data: encode(text) }));
  }
  function finish(code = 0) {
    if (finished) return;
    finished = true;
    clearInterval(timer);
    release();
    if (socket.readyState === 1) {
      socket.send(JSON.stringify({ type: 'term_exit', exit_code: code }));
      socket.close(1000, 'mock shell exited');
    }
  }
  function stopProgram() {
    clearInterval(timer);
    timer = undefined;
    program = '';
    write('\x1b[?25h');
  }
  function execute(command: string) {
    const [name, ...args] = command.split(/\s+/);
    switch (name.toLowerCase()) {
      case '':
        break;
      case 'help':
        write(
          'Mock terminal — simulated commands only\r\nhelp, pwd, cd, ls/dir, whoami, hostname, uname/ver, date, echo, export/set, env, cat, df, free, stty size, top, ping, sleep, clear/cls, exit\r\nCtrl+C stops a running program; ↑/↓ browse command history.\r\n',
        );
        break;
      case 'pwd':
        write(cwd + '\r\n');
        break;
      case 'cd': {
        const target =
          args
            .filter((a) => a.toLowerCase() !== '/d')
            .join(' ')
            .replace(/^['"]|['"]$/g, '') ||
          (windows ? 'C:\\Users\\Administrator' : '/root');
        cwd = windows
          ? win32.resolve(cwd, target)
          : posix.resolve(cwd, target === '~' ? '/root' : target);
        break;
      }
      case 'ls':
      case 'dir':
        write(
          windows
            ? ` Directory of ${cwd}\r\n\r\n11/20/2026  10:32 AM    <DIR>          logs\r\n11/20/2026  10:32 AM           1280  config.json\r\n11/20/2026  10:32 AM            512  README.txt\r\n`
            : '\x1b[34mconfig/  logs/  scripts/\x1b[0m  README.md  probex-state.json\r\n',
        );
        break;
      case 'whoami':
        write((windows ? 'Administrator' : 'root') + '\r\n');
        break;
      case 'hostname':
        write(node.info.hostname + '\r\n');
        break;
      case 'uname':
      case 'ver':
        write(
          `${node.info.os} ${node.info.hostname} ${node.info.kernel} ${node.info.arch}\r\n`,
        );
        break;
      case 'date':
        write(new Date().toLocaleString('zh-CN') + '\r\n');
        break;
      case 'echo':
        write(
          args
            .join(' ')
            .replace(/^['"]|['"]$/g, '')
            .replace(/\$(\w+)|%(\w+)%/g, (_, unix, win) => {
              const key = unix || win;
              return key === 'PWD' || key === 'CD'
                ? cwd
                : environment[key] || '';
            }) + '\r\n',
        );
        break;
      case 'export':
      case 'set': {
        const assignment = args.join(' '),
          separator = assignment.indexOf('=');
        if (separator !== -1)
          environment[assignment.slice(0, separator)] = assignment
            .slice(separator + 1)
            .replace(/^['"]|['"]$/g, '');
        break;
      }
      case 'env':
        write(
          `TERM=xterm-256color\r\nPWD=${cwd}\r\nPROBEX_MODE=mock\r\n` +
            Object.entries(environment)
              .map(([key, value]) => `${key}=${value}\r\n`)
              .join(''),
        );
        break;
      case 'cat':
        write(
          `NAME="${node.info.platform}"\r\nHOSTNAME="${node.info.hostname}"\r\nPROBEX_MODE="mock"\r\n`,
        );
        break;
      case 'df':
        write(
          'Filesystem      Size  Used Avail Use% Mounted on\r\n/dev/root        32G   12G   20G  38% /\r\ntmpfs           256M   18M  238M   7% /tmp\r\n',
        );
        break;
      case 'free':
        write(
          '              total        used        free\r\nMem:         524288      183500      340788\r\nSwap:             0           0           0\r\n',
        );
        break;
      case 'stty':
        write(`${rows} ${cols}\r\n`);
        break;
      case 'clear':
      case 'cls':
        write('\x1b[2J\x1b[H');
        break;
      case 'top': {
        program = 'top';
        let tick = 0;
        const draw = () => {
          tick++;
          write(
            `\x1b[?25l\x1b[2J\x1b[Htop - ${new Date().toLocaleTimeString()}    MOCK ${node.name}\r\nTasks: 58 total, 1 running, 57 sleeping\r\n%Cpu(s): ${(12 + Math.sin(tick) * 4).toFixed(1)} us, 2.1 sy, 85.9 id\r\nMiB Mem: 512.0 total, 332.8 free, 179.2 used\r\n\r\n\x1b[7m  PID USER      CPU%  MEM% COMMAND                 \x1b[0m\r\n 1280 root       1.2   1.8 probex\r\n  734 root       0.3   0.4 dnsmasq\r\n    1 root       0.0   0.2 init\r\n\r\nPress q or Ctrl+C to return.\r\n`,
          );
        };
        draw();
        timer = setInterval(draw, 1000);
        break;
      }
      case 'ping': {
        program = 'ping';
        let seq = 0;
        write('PING 127.0.0.1 (127.0.0.1): 56 data bytes [MOCK]\r\n');
        timer = setInterval(
          () =>
            write(
              `64 bytes from 127.0.0.1: icmp_seq=${seq++} ttl=64 time=0.042 ms\r\n`,
            ),
          1000,
        );
        break;
      }
      case 'sleep':
        program = 'sleep';
        timer = setTimeout(
          () => {
            stopProgram();
            write(prompt());
          },
          (Number(args[0]) || 5) * 1000,
        );
        break;
      case 'exit':
        finish(Number(args[0]) || 0);
        return;
      default:
        write(
          `Mock terminal: "${name}" is not simulated. Type help for supported commands.\r\n`,
        );
    }
    if (!program && !finished) write(prompt());
  }
  write(
    `\x1b[36mProbex Mock Terminal\x1b[0m — ${node.info.platform} / ${node.info.arch}\r\n模拟会话，不执行本机或设备命令。Type help for available commands.\r\n\r\n${prompt()}`,
  );
  socket.on('message', (raw) => {
    const message = JSON.parse(raw.toString());
    if (message.type === 'close') {
      finish();
      return;
    }
    if (message.type === 'resize') {
      cols = message.cols;
      rows = message.rows;
      return;
    }
    if (message.type !== 'input') return;
    const text = Buffer.from(message.data, 'base64').toString();
    if (!program && (text === '\x1b[A' || text === '\x1b[B')) {
      historyIndex = Math.max(
        0,
        Math.min(history.length, historyIndex + (text === '\x1b[A' ? -1 : 1)),
      );
      line = history[historyIndex] || '';
      write('\r\x1b[2K' + prompt() + line);
      return;
    }
    if (text.startsWith('\x1b')) return;
    for (const char of text.replace(/\r\n/g, '\r')) {
      if (char === '\x03') {
        stopProgram();
        line = '';
        write('^C\r\n' + prompt());
      } else if (program === 'top' && char === 'q') {
        stopProgram();
        write('\r\n' + prompt());
      } else if (program) continue;
      else if (char === '\r' || char === '\n') {
        write('\r\n');
        const command = line.trim();
        line = '';
        if (command) history.push(command);
        historyIndex = history.length;
        execute(command);
      } else if (char === '\x7f' || char === '\b') {
        if (line) {
          line = Array.from(line).slice(0, -1).join('');
          write('\b \b');
        }
      } else if (char === '\x0c') write('\x1b[2J\x1b[H' + prompt() + line);
      else if (char >= ' ') {
        line += char;
        write(char);
      }
      if (finished) break;
    }
  });
  socket.on('close', () => finish());
}
