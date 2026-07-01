package game

import (
	"time"

	"shift/internal/idgen"
	"shift/internal/store"
)

type ActionType string

const (
	ActionSetReady    ActionType = "set_ready"
	ActionLeaveRoom   ActionType = "leave_room"
	ActionSubmitSteal ActionType = "submit_steal"
	ActionSubmitGuess ActionType = "submit_guess"
	ActionResync      ActionType = "resync"
)

type Action struct {
	From     string
	Type     ActionType
	ObjectID int
}

// Event is an outbound message the Room wants delivered over the socket layer.
// ToUserID == "" means "broadcast to both players in this room".
type Event struct {
	ToUserID string
	Type     string
	Payload  map[string]any
}

// Room mirrors Section 3.6's GameRoom + the room-actor pattern described throughout
// Section 6: one goroutine per active room, owning all mutable state, processing
// actions sequentially over a channel — no locks needed.
type Room struct {
	ID         string
	PlayerA    string
	PlayerB    string
	ReadyA     bool
	ReadyB     bool
	Ranked     bool
	Difficulty string
	Mode       string
	Status     string // waiting | in_progress | completed | abandoned
	State      *GameState

	Actions chan Action
	Events  chan Event
	Done    chan struct{}

	st        store.Store
	cfg       DifficultyConfig
	timer     *time.Timer
	startedAt time.Time
}

func NewRoom(id, playerA, playerB string, ranked bool, difficulty, mode string, st store.Store) *Room {
	return &Room{
		ID:         id,
		PlayerA:    playerA,
		PlayerB:    playerB,
		Ranked:     ranked,
		Difficulty: difficulty,
		Mode:       mode,
		Status:     "waiting",
		Actions:    make(chan Action, 16),
		Events:     make(chan Event, 64),
		Done:       make(chan struct{}),
		st:         st,
		cfg:        ConfigFor(mode, difficulty),
	}
}

func (r *Room) opponent(userID string) string {
	if userID == r.PlayerA {
		return r.PlayerB
	}
	return r.PlayerA
}

func (r *Room) emit(to, typ string, payload map[string]any) {
	select {
	case r.Events <- Event{ToUserID: to, Type: typ, Payload: payload}:
	default:
		// Events channel is generously buffered; a full buffer means the hub has
		// stopped draining (room is being torn down) — drop rather than block.
	}
}

func (r *Room) roomStatePayload() map[string]any {
	return map[string]any{
		"roomId":     r.ID,
		"playerA":    r.PlayerA,
		"playerB":    r.PlayerB,
		"readyA":     r.ReadyA,
		"readyB":     r.ReadyB,
		"ranked":     r.Ranked,
		"difficulty": r.Difficulty,
		"mode":       r.Mode,
		"status":     r.Status,
	}
}

func (r *Room) broadcastRoomState() {
	r.emit("", "room_state", r.roomStatePayload())
}

// Run is the room actor's main loop. Call it in its own goroutine.
func (r *Room) Run() {
	r.timer = time.NewTimer(24 * time.Hour) // idle placeholder until StartMatch arms it
	r.timer.Stop()
	defer close(r.Done)

	for {
		select {
		case a, ok := <-r.Actions:
			if !ok {
				return
			}
			r.handleAction(a)
			if r.Status == "completed" || r.Status == "abandoned" {
				r.stopTimer()
				return
			}
		case <-r.timer.C:
			r.handleTimeout()
			if r.Status == "completed" || r.Status == "abandoned" {
				return
			}
		}
	}
}

func (r *Room) stopTimer() {
	if r.timer != nil {
		r.timer.Stop()
	}
}

func (r *Room) armTimer(d time.Duration) {
	r.stopTimer()
	r.timer.Reset(d)
}

func (r *Room) handleAction(a Action) {
	switch a.Type {
	case ActionResync:
		r.broadcastRoomState()
		if r.State != nil {
			r.sendCurrentPhase(a.From)
		}
	case ActionSetReady:
		if a.From == r.PlayerA {
			r.ReadyA = true
		} else if a.From == r.PlayerB {
			r.ReadyB = true
		}
		r.broadcastRoomState()
		if r.ReadyA && r.ReadyB && r.Status == "waiting" {
			r.startMatch()
		}
	case ActionLeaveRoom:
		r.handleLeave(a.From)
	case ActionSubmitSteal:
		r.handleSteal(a.From, a.ObjectID)
	case ActionSubmitGuess:
		r.handleGuess(a.From, a.ObjectID)
	}
}

func (r *Room) handleTimeout() {
	if r.State == nil {
		return
	}
	switch r.State.Phase {
	case PhaseMemorization:
		r.beginStealing()
	case PhaseStealing:
		// AFK stealer: random steal from opponent's objects (Section 6.2).
		opp := r.State.GuesserID
		objs := r.State.Objects[opp]
		if len(objs) > 0 {
			pick := objs[randN(len(objs))]
			r.applySteal(pick.ID)
		}
	case PhaseGuessing:
		// AFK guesser: auto-forfeit point to stealer (Section 6.2).
		r.resolveGuess(false, true)
	case PhaseRoundResult, PhaseSuddenDeath:
		r.advanceRound()
	}
}

// --- Section 6.1: StartMatch ---

func (r *Room) startMatch() {
	r.Status = "in_progress"
	r.startedAt = time.Now()
	cat, err := r.st.RandomCategory()
	if err != nil {
		cat = "Kitchen"
	}
	stealerFirst := r.PlayerA
	if randN(2) == 1 {
		stealerFirst = r.PlayerB
	}
	r.State = &GameState{
		Category:         cat,
		RoundNumber:      1,
		StealerID:        stealerFirst,
		GuesserID:        r.opponent(stealerFirst),
		Scores:           map[string]int{r.PlayerA: 0, r.PlayerB: 0},
		GuessesRemaining: map[string]int{},
		GuessedWrongIDs:  map[string][]int{},
	}
	r.emit("", "match_starting", map[string]any{
		"category":      cat,
		"difficulty":    r.Difficulty,
		"mode":          r.Mode,
		"objectCount":   r.cfg.Objects,
		"memorizeSecs":  r.cfg.MemorizeSecs,
		"guessAttempts": r.cfg.GuessAttempts,
		"totalRounds":   TotalRounds,
	})
	r.drawRoundObjects()
	r.beginMemorization()
}

func (r *Room) drawRoundObjects() {
	exclude := map[int]bool{}
	first, err := r.st.RandomObjects(r.State.Category, r.cfg.Objects, exclude)
	if err != nil {
		first = []store.CatalogObject{}
	}
	for _, o := range first {
		exclude[o.ID] = true
	}
	second, err := r.st.RandomObjects(r.State.Category, r.cfg.Objects, exclude)
	if err != nil {
		second = []store.CatalogObject{}
	}
	r.State.Objects = map[string][]store.CatalogObject{
		r.PlayerA: pickFor(r.PlayerA, r.State.StealerID, first, second),
		r.PlayerB: pickFor(r.PlayerB, r.State.StealerID, first, second),
	}
}

// pickFor deterministically assigns the two disjoint drawn sets to the two players.
func pickFor(player, stealer string, first, second []store.CatalogObject) []store.CatalogObject {
	if player == stealer {
		return first
	}
	return second
}

func (r *Room) beginMemorization() {
	r.State.Phase = PhaseMemorization
	r.State.StolenObjectID = nil
	r.State.PhaseDeadline = time.Now().Add(time.Duration(r.cfg.MemorizeSecs) * time.Second)
	r.armTimer(time.Duration(r.cfg.MemorizeSecs) * time.Second)

	for _, uid := range []string{r.PlayerA, r.PlayerB} {
		r.emit(uid, "phase_update", map[string]any{
			"phase":       PhaseMemorization,
			"deadline":    r.State.PhaseDeadline.UnixMilli(),
			"roundNumber": r.State.RoundNumber,
			"stealerId":   r.State.StealerID,
			"guesserId":   r.State.GuesserID,
			"yourObjects": r.State.Objects[uid],
			"scores":      r.State.Scores,
		})
	}
}

func (r *Room) beginStealing() {
	r.State.Phase = PhaseStealing
	r.State.PhaseDeadline = time.Now().Add(StealGuessAFKSecs * time.Second)
	r.armTimer(StealGuessAFKSecs * time.Second)

	r.emit(r.State.StealerID, "phase_update", map[string]any{
		"phase":            PhaseStealing,
		"deadline":         r.State.PhaseDeadline.UnixMilli(),
		"role":             "stealer",
		"opponentObjects":  r.State.Objects[r.State.GuesserID],
	})
	r.emit(r.State.GuesserID, "phase_update", map[string]any{
		"phase":    PhaseStealing,
		"deadline": r.State.PhaseDeadline.UnixMilli(),
		"role":     "waiting",
		"waiting":  "steal",
	})
}

func (r *Room) handleSteal(from string, objectID int) {
	if r.State == nil || r.State.Phase != PhaseStealing || from != r.State.StealerID {
		r.emit(from, "error", map[string]any{"code": "NOT_YOUR_TURN", "message": "it isn't your turn to steal"})
		return
	}
	valid := false
	for _, o := range r.State.Objects[r.State.GuesserID] {
		if o.ID == objectID {
			valid = true
			break
		}
	}
	if !valid {
		r.emit(from, "error", map[string]any{"code": "INVALID_OBJECT", "message": "that object isn't in your opponent's set"})
		return
	}
	r.applySteal(objectID)
}

func (r *Room) applySteal(objectID int) {
	id := objectID
	r.State.StolenObjectID = &id
	r.State.GuessesRemaining[r.State.GuesserID] = r.cfg.GuessAttempts
	r.State.GuessedWrongIDs[r.State.GuesserID] = nil
	r.beginGuessing()
}

func (r *Room) beginGuessing() {
	r.State.Phase = PhaseGuessing
	r.State.PhaseDeadline = time.Now().Add(StealGuessAFKSecs * time.Second)
	r.armTimer(StealGuessAFKSecs * time.Second)

	r.emit(r.State.GuesserID, "phase_update", map[string]any{
		"phase":            PhaseGuessing,
		"deadline":         r.State.PhaseDeadline.UnixMilli(),
		"role":             "guesser",
		"yourObjects":      r.State.Objects[r.State.GuesserID],
		"guessesRemaining": r.State.GuessesRemaining[r.State.GuesserID],
		"wrongGuesses":     r.State.GuessedWrongIDs[r.State.GuesserID],
	})
	r.emit(r.State.StealerID, "phase_update", map[string]any{
		"phase":    PhaseGuessing,
		"deadline": r.State.PhaseDeadline.UnixMilli(),
		"role":     "waiting",
		"waiting":  "guess",
	})
}

func (r *Room) handleGuess(from string, objectID int) {
	if r.State == nil || r.State.Phase != PhaseGuessing || from != r.State.GuesserID {
		r.emit(from, "error", map[string]any{"code": "NOT_YOUR_TURN", "message": "it isn't your turn to guess"})
		return
	}
	if r.State.GuessesRemaining[from] <= 0 {
		r.emit(from, "error", map[string]any{"code": "NO_ATTEMPTS_LEFT", "message": "no guesses remaining"})
		return
	}
	correct := r.State.StolenObjectID != nil && *r.State.StolenObjectID == objectID
	if correct {
		r.resolveGuess(true, false)
		return
	}
	r.State.GuessesRemaining[from]--
	r.State.GuessedWrongIDs[from] = append(r.State.GuessedWrongIDs[from], objectID)
	if r.State.GuessesRemaining[from] <= 0 {
		r.resolveGuess(false, false)
		return
	}
	// still has attempts left — stay in guessing, push updated state
	r.emit(from, "phase_update", map[string]any{
		"phase":            PhaseGuessing,
		"deadline":         r.State.PhaseDeadline.UnixMilli(),
		"role":             "guesser",
		"yourObjects":      r.State.Objects[from],
		"guessesRemaining": r.State.GuessesRemaining[from],
		"wrongGuesses":     r.State.GuessedWrongIDs[from],
		"lastGuessCorrect": false,
	})
}

// resolveGuess ends the round. correct = guesser identified the stolen object.
// afkForfeit = guesser never acted (AFK timeout auto-forfeits the point to the stealer).
func (r *Room) resolveGuess(correct bool, afkForfeit bool) {
	pointTo := r.State.StealerID
	if correct {
		pointTo = r.State.GuesserID
	}
	r.State.Scores[pointTo]++

	stolenObj := store.CatalogObject{}
	if r.State.StolenObjectID != nil {
		for _, o := range r.State.Objects[r.State.GuesserID] {
			if o.ID == *r.State.StolenObjectID {
				stolenObj = o
				break
			}
		}
	}
	result := RoundResult{
		RoundNumber:  r.State.RoundNumber,
		StealerID:    r.State.StealerID,
		GuesserID:    r.State.GuesserID,
		StolenObject: stolenObj,
		Correct:      correct,
		PointTo:      pointTo,
	}
	r.State.RoundHistory = append(r.State.RoundHistory, result)
	r.State.Phase = PhaseRoundResult
	r.State.PhaseDeadline = time.Now().Add(RoundResultPause * time.Second)
	r.armTimer(RoundResultPause * time.Second)

	r.emit("", "round_result", map[string]any{
		"roundNumber":  result.RoundNumber,
		"stolenObject": result.StolenObject,
		"correct":      result.Correct,
		"pointTo":      result.PointTo,
		"afkForfeit":   afkForfeit,
		"scores":       r.State.Scores,
	})
}

// --- Section 6.5: AdvanceRound ---

func (r *Room) advanceRound() {
	if r.State.SuddenDeath {
		r.finishMatch()
		return
	}
	if r.State.RoundNumber < TotalRounds {
		r.State.RoundNumber++
		r.State.StealerID, r.State.GuesserID = r.State.GuesserID, r.State.StealerID
		r.drawRoundObjects()
		r.beginMemorization()
		return
	}
	// Round 10 just completed.
	if r.State.Scores[r.PlayerA] == r.State.Scores[r.PlayerB] {
		r.State.SuddenDeath = true
		r.State.RoundNumber++
		r.State.StealerID, r.State.GuesserID = r.State.GuesserID, r.State.StealerID
		r.drawRoundObjects()
		r.emit("", "phase_update", map[string]any{"phase": PhaseSuddenDeath, "roundNumber": r.State.RoundNumber})
		r.beginMemorization()
		return
	}
	r.finishMatch()
}

// --- Section 6.6: ReportMatchResult ---

func (r *Room) finishMatch() {
	r.State.Phase = PhaseCompleted
	r.Status = "completed"
	r.stopTimer()

	scoreA := r.State.Scores[r.PlayerA]
	scoreB := r.State.Scores[r.PlayerB]
	winnerID, loserID := r.PlayerA, r.PlayerB
	if scoreB > scoreA {
		winnerID, loserID = r.PlayerB, r.PlayerA
	}

	payload := map[string]any{
		"winnerId":  winnerID,
		"scores":    r.State.Scores,
		"ranked":    r.Ranked,
	}

	if r.Ranked {
		winner, err1 := r.st.GetUserByID(winnerID)
		loser, err2 := r.st.GetUserByID(loserID)
		if err1 == nil && err2 == nil {
			oldWinnerRank, oldLoserRank := winner.Rank, loser.Rank
			oldWinnerLevel, oldLoserLevel := winner.Level, loser.Level

			winner.Wins++
			winner.MatchesPlayed++
			winner.Rank = RankForWins(winner.Wins)
			winner.Level = LevelForWins(winner.Wins)
			winner.WinRate = WinRate(winner.Wins, winner.MatchesPlayed)

			loser.Losses++
			loser.MatchesPlayed++
			loser.WinRate = WinRate(loser.Wins, loser.MatchesPlayed)

			m := &store.Match{
				ID:          idgen.New(),
				PlayerAID:   r.PlayerA,
				PlayerBID:   r.PlayerB,
				WinnerID:    winnerID,
				LoserID:     loserID,
				Ranked:      true,
				Difficulty:  r.Difficulty,
				Mode:        r.Mode,
				FinalScoreA: scoreA,
				FinalScoreB: scoreB,
				StartedAt:   r.startedAt,
				EndedAt:     time.Now(),
			}
			if err := r.st.ReportMatchResult(m, winner, loser); err == nil {
				payload["winnerRankUp"] = winner.Rank != oldWinnerRank
				payload["winnerLevelUp"] = winner.Level != oldWinnerLevel
				payload["winnerNewRank"] = winner.Rank
				payload["winnerNewLevel"] = winner.Level
				payload["loserRankUp"] = loser.Rank != oldLoserRank
				payload["loserLevelUp"] = loser.Level != oldLoserLevel
			}
		}
	}

	r.emit("", "match_complete", payload)
}

func (r *Room) handleLeave(from string) {
	if r.Status != "in_progress" || r.State == nil {
		r.Status = "abandoned"
		r.emit(r.opponent(from), "error", map[string]any{"code": "OPPONENT_LEFT", "message": "your opponent left the room"})
		return
	}
	// In-progress departure: remaining player wins immediately (Section 6.6 still runs
	// for Ranked matches so stats stay consistent; Casual matches just end).
	winner := r.opponent(from)
	if r.State.Scores == nil {
		r.State.Scores = map[string]int{r.PlayerA: 0, r.PlayerB: 0}
	}
	if r.State.Scores[winner] <= r.State.Scores[from] {
		r.State.Scores[winner] = r.State.Scores[from] + 1
	}
	r.finishMatch()
}

func (r *Room) sendCurrentPhase(to string) {
	if r.State == nil {
		return
	}
	switch {
	case r.State.Phase == PhaseMemorization:
		r.emit(to, "phase_update", map[string]any{
			"phase":       PhaseMemorization,
			"deadline":    r.State.PhaseDeadline.UnixMilli(),
			"roundNumber": r.State.RoundNumber,
			"stealerId":   r.State.StealerID,
			"guesserId":   r.State.GuesserID,
			"yourObjects": r.State.Objects[to],
			"scores":      r.State.Scores,
		})
	case r.State.Phase == PhaseStealing && to == r.State.StealerID:
		r.emit(to, "phase_update", map[string]any{
			"phase":           PhaseStealing,
			"deadline":        r.State.PhaseDeadline.UnixMilli(),
			"role":            "stealer",
			"opponentObjects": r.State.Objects[r.State.GuesserID],
		})
	case r.State.Phase == PhaseGuessing && to == r.State.GuesserID:
		r.emit(to, "phase_update", map[string]any{
			"phase":            PhaseGuessing,
			"deadline":         r.State.PhaseDeadline.UnixMilli(),
			"role":             "guesser",
			"yourObjects":      r.State.Objects[to],
			"guessesRemaining": r.State.GuessesRemaining[to],
			"wrongGuesses":     r.State.GuessedWrongIDs[to],
		})
	default:
		r.emit(to, "phase_update", map[string]any{
			"phase":    r.State.Phase,
			"deadline": r.State.PhaseDeadline.UnixMilli(),
			"role":     "waiting",
		})
	}
}

func randN(n int) int {
	// small, non-cryptographic-quality is fine for gameplay coin flips/AFK picks;
	// reuse store's crypto-backed shuffle helper indirectly isn't exported, so
	// this package keeps its own trivial helper.
	if n <= 0 {
		return 0
	}
	return int(time.Now().UnixNano() % int64(n))
}
