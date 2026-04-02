package main

func browserTokenDiscoveryJS() string {
	return `function normalizeTruthSocialToken(value) {
  if (typeof value !== 'string') {
    return '';
  }
  const token = value.trim();
  if (!token) {
    return '';
  }
  const patterns = [
    /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/,
    /^LMIN[A-Za-z0-9._-]{12,}$/,
    /^[A-Za-z0-9._-]{32,}$/,
    /^[A-Za-z0-9+/=._-]{40,}$/
  ];
  for (const pattern of patterns) {
    if (pattern.test(token)) {
      return token;
    }
  }
  return '';
}

function addTruthSocialTokenCandidate(candidates, value) {
  const token = normalizeTruthSocialToken(value);
  if (token) {
    candidates.push(token);
  }
}

function scanTruthSocialObject(value, candidates, seen, depth) {
  if (value === null || value === undefined || depth > 6) {
    return;
  }
  const valueType = typeof value;
  if (valueType === 'string') {
    addTruthSocialTokenCandidate(candidates, value);
    return;
  }
  if (valueType === 'number' || valueType === 'boolean' || valueType === 'bigint') {
    return;
  }
  if (valueType !== 'object') {
    return;
  }
  if (seen.has(value)) {
    return;
  }
  seen.add(value);
  if (Array.isArray(value)) {
    for (const item of value) {
      scanTruthSocialObject(item, candidates, seen, depth + 1);
    }
    return;
  }

  for (const [key, item] of Object.entries(value)) {
    if (/(token|auth|bearer|access|session)/i.test(key)) {
      addTruthSocialTokenCandidate(candidates, item);
    }
    scanTruthSocialObject(item, candidates, seen, depth + 1);
  }
}

function scanTruthSocialStorageArea(storage) {
  const candidates = [];
  if (!storage || typeof storage.length !== 'number' || typeof storage.getItem !== 'function') {
    return candidates;
  }

  const seen = new Set();
  for (let i = 0; i < storage.length; i += 1) {
    const key = storage.key(i) || '';
    const value = storage.getItem(key) || '';
    if (!value) {
      continue;
    }

    if (/(token|auth|bearer|access|session)/i.test(key)) {
      addTruthSocialTokenCandidate(candidates, value);
    }

    addTruthSocialTokenCandidate(candidates, value);
    try {
      scanTruthSocialObject(JSON.parse(value), candidates, seen, 0);
    } catch (e) {
    }
  }

  return candidates;
}

function scanTruthSocialWindowGlobals() {
  const candidates = [];
  const seen = new Set();
  const names = [
    '__NEXT_DATA__',
    '__INITIAL_STATE__',
    '__PRELOADED_STATE__',
    '__APOLLO_STATE__',
    '__REDUX_STATE__',
    '__STATE__',
    '__INITIAL_DATA__'
  ];

  for (const name of names) {
    try {
      if (typeof window !== 'undefined' && Object.prototype.hasOwnProperty.call(window, name)) {
        scanTruthSocialObject(window[name], candidates, seen, 0);
      }
    } catch (e) {
    }
  }

  return candidates;
}

function scanTruthSocialDocumentCookies() {
  const candidates = [];
  try {
    const cookieString = (typeof document !== 'undefined' && document.cookie) ? document.cookie : '';
    if (!cookieString) {
      return candidates;
    }
    for (const part of cookieString.split(/;\s*/)) {
      const index = part.indexOf('=');
      if (index <= 0) {
        continue;
      }
      const key = part.slice(0, index);
      const value = part.slice(index + 1);
      if (/(token|auth|bearer|access|session)/i.test(key)) {
        addTruthSocialTokenCandidate(candidates, decodeURIComponent(value || ''));
      }
    }
  } catch (e) {
  }
  return candidates;
}

function readTruthSocialBearerToken() {
  const candidates = [];
  try {
    candidates.push(...scanTruthSocialStorageArea(localStorage));
  } catch (e) {
  }
  try {
    candidates.push(...scanTruthSocialStorageArea(sessionStorage));
  } catch (e) {
  }
  try {
    candidates.push(...scanTruthSocialWindowGlobals());
  } catch (e) {
  }
  try {
    candidates.push(...scanTruthSocialDocumentCookies());
  } catch (e) {
  }

  const unique = [];
  const seen = new Set();
  for (const candidate of candidates) {
    const token = normalizeTruthSocialToken(candidate);
    if (!token || seen.has(token)) {
      continue;
    }
    seen.add(token);
    unique.push(token);
  }

  return unique.length > 0 ? unique[0] : '';
}`
}
