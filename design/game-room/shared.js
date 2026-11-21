/* Find the Human — game room mockups: shared mock data + helpers.
   Design only. No game logic. Mock data never contains usernames; only aliases (SPEC §2.1). */

const HEX = {ink:'#1b1220',wood_d:'#2a1a14',wood:'#5a3524',wood_l:'#8a5a36',parch:'#f1e2bc',parch_d:'#d4b983',gold:'#e9b44c',red:'#c2503c',green:'#6fa04a',blue:'#3f78a8',purple:'#7d56a0',teal:'#3e9c94',grey:'#8c8579',white:'#fff6e0',pink:'#d9739a',sky:'#8fc3d9'};
const BODY_COLORS = ['red','blue','green','purple','teal','pink','gold','sky'];

function mulberry32(a){
  return function(){
    a |= 0; a = a + 0x6D2B79F5 | 0;
    let t = Math.imul(a ^ a >>> 15, 1 | a);
    t = t + Math.imul(t ^ t >>> 7, 61 | t) ^ t;
    return ((t ^ t >>> 14) >>> 0) / 4294967296;
  };
}

/* Procedural symmetric 16x16 creature from a seed (SPEC §8). Palette-limited. */
function drawSprite(cv, seed){
  const r = mulberry32(seed + 1);
  const ctx = cv.getContext('2d');
  ctx.imageSmoothingEnabled = false;
  ctx.clearRect(0, 0, 16, 16);
  const body = HEX[BODY_COLORS[Math.floor(r() * BODY_COLORS.length)]];
  const accent = HEX[BODY_COLORS[Math.floor(r() * BODY_COLORS.length)]];
  const half = [];
  for (let y = 0; y < 16; y++){
    half[y] = [];
    for (let x = 0; x < 8; x++){
      const on = y >= 3 && y <= 14 && r() < 0.92 - Math.abs(y - 8) * 0.04 - x * 0.03;
      half[y][x] = on ? (r() < 0.18 ? accent : body) : null;
    }
  }
  if (r() < 0.6){ half[2][1] = accent; half[2][6] = accent; }
  half[6][3] = HEX.white;            // eye
  half[10][3] = HEX.ink;             // mouth
  for (let y = 0; y < 16; y++){
    for (let x = 0; x < 16; x++){
      const c = x < 8 ? half[y][x] : half[y][15 - x];
      if (c){ ctx.fillStyle = c; ctx.fillRect(x, y, 1, 1); }
    }
  }
}
function initSprites(root){
  root.querySelectorAll('canvas[data-seed]').forEach(c => drawSprite(c, +c.dataset.seed));
}

/* ---------- phases (SPEC §2.2) ---------- */
const PHASE_ORDER = ['INTRO','ASK','ANSWER','REVEAL','VOTE','RESULT'];
const PHASES = {
  INTRO:  {title:'Introductions',     hint:'Say hi in 160 characters. Optional.',              max:160, secs:60, input:'intro'},
  ASK:    {title:'Ask the table',     hint:'One question for everyone. 160 characters.',       max:160, secs:45, input:'ask'},
  ANSWER: {title:'Answer the table',  hint:'Answer each question. Skip any. 200 characters.',  max:200, secs:90, input:'answer'},
  REVEAL: {title:'Answers are in',   hint:'Everyone sees these at the same time.',            max:0,   secs:30, input:null},
  VOTE:   {title:'Who is the human?', hint:'Pick one seat. Locked once cast.',                 max:0,   secs:45, input:'vote'},
  RESULT: {title:'Game over',        hint:'Seats are revealed.',                              max:0,   secs:0,  input:null}
};

/* ---------- mock table (aliases only) ---------- */
const SEATS = [
  {alias:'Amber Owl',    seed:11, you:true},
  {alias:'Blue Fox',     seed:23},
  {alias:'Rusty Badger', seed:37},
  {alias:'Velvet Moth',  seed:41},
  {alias:'Iron Hare',    seed:59}
];
// Result-only. Never rendered before RESULT (SPEC invariant 1).
const REVEAL_SEATS = [
  {kind:'human'},
  {kind:'human'},
  {kind:'ai', model:'qwen-3.6'},
  {kind:'ai', model:'gemma-4'},
  {kind:'ai', model:'mistral-small'}
];
// Suspicion markers, private to the owner (SPEC §4). Own seat cannot be marked.
const MARKS = [
  {mark:'none'},
  {mark:'?', note:'sounds like a night nurse?'},
  {mark:'!', note:'too fast to be a person'},
  {mark:'none'},
  {mark:'none'}
];
const DRAFTS = {ANSWER:'crime. definitely crime, fight me'};
const INTROS = [
  {seat:1, text:'hi im blue. night shift nurse, here for the cider'},
  {seat:2, text:'rusty. i fix forklifts.'},
  {seat:3, text:'hello :) i like old films and bad coffee'},
  {seat:4, text:null},
  {seat:0, text:'amber. just got here, pls be nice'}
];
const QUESTIONS = [
  {id:'q1', seat:1, text:'whats the worst thing youve microwaved'},
  {id:'q2', seat:3, text:'is pineapple on pizza a crime or a cry for help'},
  {id:'q3', seat:0, text:'what time is it where you are'}
];
const ANSWERS = {
  q1: [{seat:2, text:'a fork. dont ask'}, {seat:3, text:'a whole egg. kitchen smelled for a week'}, {seat:4, text:null}, {seat:0, text:'half a bagel. long story'}],
  q2: [{seat:1, text:'crime. always crime'}, {seat:2, text:'cry for help but tasty'}, {seat:4, text:'idk what a pizza is anymore lol'}, {seat:0, text:null}],
  q3: []
};

/* ---------- helpers ---------- */
function esc(s){
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
const MK = {none:['—','none'], '?':['?','q'], '!':['!','x'], human:['HUMAN','human'], ai:['AI','ai']};

function marksHTML(i){
  const cur = MARKS[i].mark;
  return `<div class="marks" role="group" aria-label="Suspicion marker">${Object.keys(MK).map(k =>
    `<button class="mk${k === cur ? ' on' : ''}" data-mark="${esc(k)}">${MK[k][0]}</button>`).join('')}</div>`;
}

/* o: {reveal, selectable, marks} */
function seatHTML(i, o = {}){
  const s = SEATS[i], rv = o.reveal ? REVEAL_SEATS[i] : null, m = MARKS[i];
  const tag = rv
    ? `<span class="tag ${rv.kind}">${rv.kind === 'ai' ? 'AI · ' + esc(rv.model) : 'HUMAN'}</span>`
    : (s.you ? '<span class="tag you">YOU</span>' : '');
  const cls = ['seat', s.you ? 'me' : '', o.selectable && !s.you ? 'selectable' : ''].join(' ').trim();
  return `<div class="${cls}" data-seat="${i}">
    <canvas class="sprite" width="16" height="16" data-seed="${s.seed}" aria-hidden="true"></canvas>
    <div class="alias">${esc(s.alias)}</div>${tag}
    ${!s.you && o.marks !== false ? marksHTML(i) : ''}
    ${m.note && !s.you ? `<div class="note">“${esc(m.note)}”</div>` : ''}
  </div>`;
}

/* Flat list of transcript entries visible in a phase. Sealed content stays hidden until its phase reveals it. */
function transcriptEntries(phase){
  const out = [];
  const from = list => list.includes(phase);
  if (from(['ASK','ANSWER','REVEAL','VOTE','RESULT']))
    INTROS.forEach(x => out.push({t:'intro', seat:x.seat, text:x.text}));
  if (from(['ANSWER','REVEAL','VOTE','RESULT']))
    QUESTIONS.forEach(q => {
      out.push({t:'q', seat:q.seat, text:q.text, qid:q.id});
      if (from(['REVEAL','VOTE','RESULT']))
        ANSWERS[q.id].forEach(a => out.push({t:'a', seat:a.seat, text:a.text, qid:q.id}));
    });
  return out;
}
function entryHTML(e){
  const who = esc(SEATS[e.seat].alias);
  const say = e.text ? esc(e.text) : '—';
  if (e.t === 'q')  return `<div class="ent q"><span class="tagline">${who} asks</span><div class="say">${esc(e.text)}</div></div>`;
  if (e.t === 'a')  return `<div class="ent a${e.text ? '' : ' skip'}"><span class="who">${who}</span><span class="say">${say}</span></div>`;
  return `<div class="ent intro"><span class="who">${who}</span><span class="say">${say}</span></div>`;
}

/* What the composer shows in this phase. */
function composeData(phase){
  const p = PHASES[phase];
  if (p.input === 'vote') return {type:'vote'};
  if (!p.input) return {type:'info', title:p.title, hint:p.hint};
  if (p.input === 'answer'){
    const q = QUESTIONS[1];
    return {type:'text', label:'Answer · ' + SEATS[q.seat].alias, body:q.text, max:p.max, draft:DRAFTS[phase] || ''};
  }
  return {type:'text', label: p.input === 'intro' ? 'Your intro' : 'Your question for the table', body:'', max:p.max, draft:DRAFTS[phase] || ''};
}
function composeHTML(c){
  if (c.type === 'info') return `<div class="kick">${esc(c.title)}</div><p class="hint">${esc(c.hint)}</p>`;
  if (c.type === 'vote') return `<div class="kick">Pick the human</div><p class="hint">Click a seat at the table. Locked once cast.</p><button class="btn" data-confirm disabled>Confirm vote</button>`;
  return `<div class="kick">${esc(c.label)}</div>${c.body ? `<blockquote class="qq">“${esc(c.body)}”</blockquote>` : ''}
    <textarea class="ta" maxlength="${c.max}" aria-label="Your message">${esc(c.draft)}</textarea>
    <div class="row"><span class="count">${c.draft.length}/${c.max}</span><span class="grow"></span><button class="btn ghost">Skip</button><button class="btn">Send</button></div>`;
}

let __timer = null;
function startTimer(root, secs){
  clearInterval(__timer);
  const out = root.querySelector('[data-timer]'), bar = root.querySelector('[data-bar]');
  if (!secs){ if (out) out.textContent = '--:--'; if (bar) bar.style.width = '0%'; return; }
  let left = secs;
  const tick = () => {
    if (out) out.textContent = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`;
    if (bar) bar.style.width = (left / secs * 100) + '%';
    if (left > 0) left--;
  };
  tick();
  __timer = setInterval(tick, 1000);
}
function afterRender(root, phase){ initSprites(root); startTimer(root, PHASES[phase].secs); }

/* Design-only phase switcher bar + click behaviour for marks and vote picks. */
function mountControls(render){
  const bar = document.createElement('div');
  bar.className = 'mockup-bar';
  bar.innerHTML = `<span>MOCKUP · GAME ROOM</span><label>PHASE <select>${PHASE_ORDER.map(p => `<option>${p}</option>`).join('')}</select></label>`;
  document.body.prepend(bar);
  const sel = bar.querySelector('select');
  sel.onchange = () => render(sel.value);
  render(sel.value);
}
document.addEventListener('click', e => {
  const mk = e.target.closest('.mk');
  if (mk){
    mk.parentElement.querySelectorAll('.mk').forEach(b => b.classList.remove('on'));
    mk.classList.add('on');
    return;
  }
  const seat = e.target.closest('.seat.selectable');
  if (seat){
    document.querySelectorAll('.seat.picked').forEach(x => x.classList.remove('picked'));
    seat.classList.add('picked');
    document.querySelectorAll('[data-confirm]').forEach(b => b.disabled = false);
  }
});
