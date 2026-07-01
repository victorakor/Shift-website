// ai.js — Play vs Computer is entirely client-side (Section 6.7): pulls a random
// object set from /api/object-catalog/random once, then runs all 10 rounds locally.
// No WS room, no ReportMatchResult call — practice mode never touches rank/wins.

const STANDARD_DIFFICULTY = {
  easy:   { objects: 10, memorizeSecs: 10, guessAttempts: 3, aiAccuracy: 0.35 },
  medium: { objects: 20, memorizeSecs: 8,  guessAttempts: 2, aiAccuracy: 0.55 },
  hard:   { objects: 25, memorizeSecs: 5,  guessAttempts: 1, aiAccuracy: 0.75 },
};

class VsComputerGame {
  constructor(difficulty, root) {
    this.difficulty = difficulty;
    this.cfg = STANDARD_DIFFICULTY[difficulty];
    this.root = root;
    this.round = 1;
    this.totalRounds = 10;
    this.scores = { you: 0, ai: 0 };
    this.youStealFirst = Math.random() < 0.5;
  }

  async start() {
    await this.playRound();
  }

  async drawObjects() {
    const catRes = await apiFetch('/api/object-catalog/categories');
    const category = catRes.categories[Math.floor(Math.random() * catRes.categories.length)];
    const res = await apiFetch(`/api/object-catalog/random?category=${encodeURIComponent(category)}&count=${this.cfg.objects * 2}`);
    const all = res.objects;
    return { yourObjects: all.slice(0, this.cfg.objects), aiObjects: all.slice(this.cfg.objects, this.cfg.objects * 2) };
  }

  async playRound() {
    const { yourObjects, aiObjects } = await this.drawObjects();
    this.yourObjects = yourObjects;
    this.aiObjects = aiObjects;
    const youAreStealer = (this.round % 2 === 1) ? this.youStealFirst : !this.youStealFirst;
    this.youAreStealer = youAreStealer;
    this.renderMemorization();
  }

  header() {
    return el('div', { style: 'display:flex; justify-content:space-between; align-items:center; margin-bottom:16px;' }, [
      el('div', { class: 'score-value' }, String(this.scores.you)),
      el('div', { class: 'muted' }, `Round ${this.round} / ${this.totalRounds} — You vs Computer`),
      el('div', { class: 'score-value' }, String(this.scores.ai)),
    ]);
  }

  objectCardEl(obj, opts = {}) {
    const wrap = el('div', { class: 'object-card-flip vault-card' + (opts.flipped ? ' is-flipped' : ''), style: 'height:100px; padding:0;' });
    const inner = el('div', { class: 'object-card-inner' });
    const front = el('div', { class: 'object-card-face front' + (opts.selectable ? ' selectable' : '') + (opts.wrong ? ' wrong' : '') }, [
      el('div', { class: 'emoji' }, obj.emoji || '📦'),
      el('div', { class: 'obj-name' }, obj.name || ''),
    ]);
    if (opts.selectable) front.addEventListener('click', () => opts.onSelect(obj));
    const back = el('div', { class: 'object-card-face back' });
    inner.appendChild(front); inner.appendChild(back); wrap.appendChild(inner);
    return wrap;
  }

  renderMemorization() {
    this.root.innerHTML = '';
    this.root.appendChild(this.header());
    this.root.appendChild(el('h3', {}, this.youAreStealer ? 'Memorize the computer\'s objects (you steal next)' : 'Memorize your objects (computer steals next)'));
    const timerLabel = el('div', { class: 'mono muted', style: 'margin-bottom:8px;' }, '');
    this.root.appendChild(timerLabel);
    const objects = this.youAreStealer ? this.aiObjects : this.yourObjects;
    const grid = el('div', { class: 'object-grid' });
    objects.forEach(o => grid.appendChild(this.objectCardEl(o)));
    this.root.appendChild(grid);

    let remaining = this.cfg.memorizeSecs;
    timerLabel.textContent = remaining + 's';
    const iv = setInterval(() => {
      remaining--;
      timerLabel.textContent = Math.max(0, remaining) + 's';
      if (remaining <= 0) { clearInterval(iv); this.beginSteal(); }
    }, 1000);
  }

  beginSteal() {
    if (this.youAreStealer) {
      this.renderYourSteal();
    } else {
      // AI steals instantly.
      const pick = this.aiObjects[Math.floor(Math.random() * this.aiObjects.length)];
      this.stolen = pick;
      this.renderYourGuess();
    }
  }

  renderYourSteal() {
    this.root.innerHTML = '';
    this.root.appendChild(this.header());
    this.root.appendChild(el('h3', {}, 'Pick an object to steal from the computer'));
    const grid = el('div', { class: 'object-grid' });
    this.aiObjects.forEach(o => grid.appendChild(this.objectCardEl(o, {
      selectable: true, onSelect: (obj) => { this.stolen = obj; this.renderAiGuess(); },
    })));
    this.root.appendChild(grid);
  }

  renderAiGuess() {
    this.root.innerHTML = '';
    this.root.appendChild(this.header());
    this.root.appendChild(el('div', { class: 'vault-card vault-card--empty pulse' }, 'Computer is guessing…'));
    setTimeout(() => {
      const correct = Math.random() < this.cfg.aiAccuracy;
      this.resolveRound(correct, 'ai');
    }, 1200);
  }

  renderYourGuess(wrongIds = new Set(), attemptsLeft = this.cfg.guessAttempts) {
    this.root.innerHTML = '';
    this.root.appendChild(this.header());
    this.root.appendChild(el('h3', {}, `Guess what the computer stole — ${attemptsLeft} attempt(s) left`));
    const grid = el('div', { class: 'object-grid' });
    this.yourObjects.forEach(o => grid.appendChild(this.objectCardEl(o, {
      selectable: !wrongIds.has(o.id), wrong: wrongIds.has(o.id),
      onSelect: (obj) => {
        if (obj.id === this.stolen.id) {
          this.resolveRound(true, 'you');
        } else {
          wrongIds.add(obj.id);
          attemptsLeft--;
          if (attemptsLeft <= 0) this.resolveRound(false, 'you');
          else this.renderYourGuess(wrongIds, attemptsLeft);
        }
      },
    })));
    this.root.appendChild(grid);
  }

  resolveRound(correct, guesserSide) {
    const pointTo = correct ? guesserSide : (guesserSide === 'you' ? 'ai' : 'you');
    this.scores[pointTo]++;
    this.root.innerHTML = '';
    this.root.appendChild(el('div', { class: `vault-card vault-card--${correct ? 'success' : 'danger'}`, style: 'text-align:center; padding:32px;' }, [
      el('div', { style: 'font-size:2.5rem;' }, this.stolen.emoji),
      el('h3', {}, this.stolen.name),
      el('div', { class: 'muted' }, correct ? 'Correctly guessed!' : 'Wrong guess'),
    ]));
    this.root.appendChild(this.header());
    setTimeout(() => this.nextRound(), 2000);
  }

  nextRound() {
    if (this.round >= this.totalRounds && this.scores.you !== this.scores.ai) {
      this.finish();
      return;
    }
    if (this.round >= this.totalRounds && this.scores.you === this.scores.ai) {
      this.totalRounds++; // sudden death, one extra round at a time
    }
    this.round++;
    this.playRound();
  }

  finish() {
    this.root.innerHTML = '';
    const youWon = this.scores.you > this.scores.ai;
    this.root.appendChild(el('div', { class: `vault-card vault-card--${youWon ? 'success' : 'danger'}`, style: 'text-align:center; padding:32px;' }, [
      el('h2', {}, youWon ? 'You win!' : 'Computer wins'),
      el('div', { class: 'score-value' }, `${this.scores.you} — ${this.scores.ai}`),
      el('div', { class: 'muted', style: 'margin-top:8px;' }, 'Practice mode — no rank change.'),
      el('a', { class: 'btn', href: '/', style: 'display:inline-block; margin-top:16px; text-decoration:none;' }, 'Back Home'),
    ]));
  }
}
