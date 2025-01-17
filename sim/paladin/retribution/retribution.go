package retribution

import (
	"github.com/wowsims/sod/sim/core"
	"github.com/wowsims/sod/sim/core/proto"
	"github.com/wowsims/sod/sim/paladin"
)

func RegisterRetributionPaladin() {
	core.RegisterAgentFactory(
		proto.Player_RetributionPaladin{},
		proto.Spec_SpecRetributionPaladin,
		func(character *core.Character, options *proto.Player) core.Agent {
			return NewRetributionPaladin(character, options)
		},
		func(player *proto.Player, spec interface{}) {
			playerSpec, ok := spec.(*proto.Player_RetributionPaladin) // I don't really understand this line
			if !ok {
				panic("Invalid spec value for Retribution Paladin!")
			}
			player.Spec = playerSpec
		},
	)
}

func NewRetributionPaladin(character *core.Character, options *proto.Player) *RetributionPaladin {
	retOptions := options.GetRetributionPaladin()

	pal := paladin.NewPaladin(character, options.TalentsString)

	ret := &RetributionPaladin{
		Paladin:     pal,
		PrimarySeal: retOptions.Options.PrimarySeal,
	}

	ret.EnableAutoAttacks(ret, core.AutoAttackOptions{
		MainHand:       ret.WeaponFromMainHand(ret.DefaultMeleeCritMultiplier()), // Set crit multiplier later when we have targets.
		AutoSwingMelee: true,
	})

	return ret
}

type RetributionPaladin struct {
	*paladin.Paladin

	PrimarySeal proto.PaladinSeal
}

func (ret *RetributionPaladin) GetPaladin() *paladin.Paladin {
	return ret.Paladin
}

func (ret *RetributionPaladin) Initialize() {
	ret.Paladin.Initialize()
}

func (ret *RetributionPaladin) Reset(sim *core.Simulation) {
	ret.Paladin.Reset(sim)
	ret.CurrentSeal = nil

	// Set the primary seal for APL actions.
	switch ret.PrimarySeal {
	case proto.PaladinSeal_Righteousness:
		ret.PrimarySealSpell = ret.Paladin.GetMaxRankSeal(ret.PrimarySeal)
	case proto.PaladinSeal_Command:
		ret.PrimarySealSpell = ret.Paladin.GetMaxRankSeal(ret.PrimarySeal)
	case proto.PaladinSeal_Martyrdom:
		ret.PrimarySealSpell = ret.Paladin.SealOfMartyrdom
	// The following provisions for when a player has chosen a seal in the UI
	// which is no longer viable based on rune/talent changes. This stops the
	// APL being hung up on conditions where the current seal APL value is nearing
	// expiry.
	case proto.PaladinSeal_NoSeal:
		ret.Paladin.CurrentSealExpiration = 100000000
	}
}
