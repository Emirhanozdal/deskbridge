function quote(value) {
  value = String(value);
  if (!value || /[\r\n\0]/.test(value)) throw Error('Invalid input setting');
  return '"' + value.replaceAll('\\', '\\\\').replaceAll('"', '\\"') + '"';
}

function inputSettings(mode, name, layoutFile) {
  let output = '[core]\ncomputerName=' + quote(name) + '\n\n';
  output += '[security]\ntlsEnabled=false\ncheckPeerFingerprints=false\n\n';
  if (mode === 'server') {
    output += '[server]\nexternalConfig=true\nexternalConfigFile=' + quote(layoutFile) + '\n';
  } else if (mode === 'client') {
    output += '[client]\nremoteHost="127.0.0.1:24801"\n';
  } else throw Error('Invalid input mode');
  return output;
}

module.exports = { inputSettings };
