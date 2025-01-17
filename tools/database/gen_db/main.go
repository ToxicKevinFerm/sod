package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/wowsims/sod/sim"
	"github.com/wowsims/sod/sim/core"
	"github.com/wowsims/sod/sim/core/proto"
	_ "github.com/wowsims/sod/sim/encounters" // Needed for preset encounters.
	"github.com/wowsims/sod/tools"
	"github.com/wowsims/sod/tools/database"
)

// To do a full re-scrape, delete the previous output file first.
// go run ./tools/database/gen_db -outDir=assets -gen=atlasloot
// go run ./tools/database/gen_db -outDir=assets -gen=wowhead-items
// go run ./tools/database/gen_db -outDir=assets -gen=wowhead-spells -maxid=31000
// go run ./tools/database/gen_db -outDir=assets -gen=wowhead-gearplannerdb
// python3 tools/scrape_runes.py assets/db_inputs/wowhead_rune_tooltips.csv

// Lastly run the following to generate db.json (ensure to delete cached versions and/or rebuild for copying of assets during local development)
// Note: This does not make network requests, only regenerates core db binary and json files from existing inputs
// go run ./tools/database/gen_db -outDir=assets -gen=db

var minId = flag.Int("minid", 1, "Minimum ID to scan for")
var maxId = flag.Int("maxid", 31000, "Maximum ID to scan for")
var outDir = flag.String("outDir", "assets", "Path to output directory for writing generated .go files.")
var genAsset = flag.String("gen", "", "Asset to generate. Valid values are 'db', 'atlasloot', 'wowhead-items', 'wowhead-spells', 'wowhead-itemdb', 'wotlk-items', and 'wago-db2-items'")

func main() {
	flag.Parse()
	if *outDir == "" {
		panic("outDir flag is required!")
	}

	dbDir := fmt.Sprintf("%s/database", *outDir)
	inputsDir := fmt.Sprintf("%s/db_inputs", *outDir)

	if *genAsset == "atlasloot" {
		db := database.ReadAtlasLootData()
		db.WriteJson(fmt.Sprintf("%s/atlasloot_db.json", inputsDir))
		return
	} else if *genAsset == "wowhead-items" {
		database.NewWowheadItemTooltipManager(fmt.Sprintf("%s/wowhead_item_tooltips.csv", inputsDir)).Fetch(int32(*minId), int32(*maxId), database.OtherItemIdsToFetch)
		return
	} else if *genAsset == "wowhead-spells" {
		database.NewWowheadSpellTooltipManager(fmt.Sprintf("%s/wowhead_spell_tooltips.csv", inputsDir)).Fetch(int32(*minId), int32(*maxId), []string{})
		return
	} else if *genAsset == "wowhead-gearplannerdb" {
		tools.WriteFile(fmt.Sprintf("%s/wowhead_gearplannerdb.txt", inputsDir), tools.ReadWebRequired("https://nether.wowhead.com/classic/data/gear-planner?dv=100"))
		return
	} else if *genAsset != "db" {
		panic("Invalid gen value")
	}

	itemTooltips := database.NewWowheadItemTooltipManager(fmt.Sprintf("%s/wowhead_item_tooltips.csv", inputsDir)).Read()
	spellTooltips := database.NewWowheadSpellTooltipManager(fmt.Sprintf("%s/wowhead_spell_tooltips.csv", inputsDir)).Read()
	runeTooltips := database.NewWowheadSpellTooltipManager(fmt.Sprintf("%s/wowhead_rune_tooltips.csv", inputsDir)).Read()
	wowheadDB := database.ParseWowheadDB(tools.ReadFile(fmt.Sprintf("%s/wowhead_gearplannerdb.txt", inputsDir)))
	atlaslootDB := database.ReadDatabaseFromJson(tools.ReadFile(fmt.Sprintf("%s/atlasloot_db.json", inputsDir)))
	// factionRestrictions := database.ParseItemFactionRestrictionsFromWagoDB(tools.ReadFile(fmt.Sprintf("%s/wago_db2_items.csv", inputsDir)))

	db := database.NewWowDatabase()
	db.Encounters = core.PresetEncounters

	for _, response := range itemTooltips {
		if response.IsEquippable() {
			// Only included items that are in wowheads gearplanner db
			// Wowhead doesn't seem to have a field/flag to signify 'not available / in game' but their gearplanner db has them filtered
			item := response.ToItemProto()
			if _, ok := wowheadDB.Items[strconv.Itoa(int(item.Id))]; ok {
				db.MergeItem(item)
			}
		}
	}
	for _, wowheadItem := range wowheadDB.Items {
		item := wowheadItem.ToProto()
		if _, ok := db.Items[item.Id]; ok {
			db.MergeItem(item)
		}
	}
	for _, item := range atlaslootDB.Items {
		if _, ok := db.Items[item.Id]; ok {
			db.MergeItem(item)
		}
	}

	for id, rune := range runeTooltips {
		db.AddRune(id, rune)
	}

	db.MergeItems(database.ItemOverrides)
	db.MergeEnchants(database.EnchantOverrides)
	db.MergeRunes(database.RuneOverrides)
	ApplyGlobalFilters(db)
	// AttachFactionInformation(db, factionRestrictions)

	leftovers := db.Clone()
	ApplyNonSimmableFilters(leftovers)
	leftovers.WriteBinaryAndJson(fmt.Sprintf("%s/leftover_db.bin", dbDir), fmt.Sprintf("%s/leftover_db.json", dbDir))

	ApplySimmableFilters(db)
	for _, enchant := range db.Enchants {
		if enchant.ItemId != 0 {
			db.AddItemIcon(enchant.ItemId, itemTooltips)
		}
		if enchant.SpellId != 0 {
			db.AddSpellIcon(enchant.SpellId, spellTooltips)
		}
	}

	for _, itemID := range database.ExtraItemIcons {
		db.AddItemIcon(itemID, itemTooltips)
	}

	for _, item := range db.Items {
		for _, source := range item.Sources {
			if crafted := source.GetCrafted(); crafted != nil {
				db.AddSpellIcon(crafted.SpellId, spellTooltips)
			}
		}

		for _, randomSuffixID := range item.RandomSuffixOptions {
			if _, exists := db.RandomSuffixes[randomSuffixID]; !exists {
				db.RandomSuffixes[randomSuffixID] = wowheadDB.RandomSuffixes[strconv.Itoa(int(randomSuffixID))].ToProto()
			}
		}
	}

	for _, spellId := range database.SharedSpellsIcons {
		db.AddSpellIcon(spellId, spellTooltips)
	}

	for _, spellIds := range GetAllTalentSpellIds(&inputsDir) {
		for _, spellId := range spellIds {
			db.AddSpellIcon(spellId, spellTooltips)
		}
	}

	for _, spellIds := range GetAllRotationSpellIds() {
		for _, spellId := range spellIds {
			db.AddSpellIcon(spellId, spellTooltips)
		}
	}

	atlasDBProto := atlaslootDB.ToUIProto()
	db.MergeZones(atlasDBProto.Zones)
	db.MergeNpcs(atlasDBProto.Npcs)

	db.WriteBinaryAndJson(fmt.Sprintf("%s/db.bin", dbDir), fmt.Sprintf("%s/db.json", dbDir))
}

// Filters out entities which shouldn't be included anywhere.
func ApplyGlobalFilters(db *database.WowDatabase) {
	db.Items = core.FilterMap(db.Items, func(_ int32, item *proto.UIItem) bool {
		if _, ok := database.ItemDenyList[item.Id]; ok {
			return false
		}

		for _, pattern := range database.DenyListNameRegexes {
			if pattern.MatchString(item.Name) {
				return false
			}
		}
		return true
	})

	// There is an 'unavailable' version of every naxx set, e.g. https://www.wowhead.com/classic/item=43728/bonescythe-gauntlets
	// heroesItems := core.FilterMap(db.Items, func(_ int32, item *proto.UIItem) bool {
	// 	return strings.HasPrefix(item.Name, "Heroes' ")
	// })
	// db.Items = core.FilterMap(db.Items, func(_ int32, item *proto.UIItem) bool {
	// 	nameToMatch := "Heroes' " + item.Name
	// 	for _, heroItem := range heroesItems {
	// 		if heroItem.Name == nameToMatch {
	// 			return false
	// 		}
	// 	}
	// 	return true
	// })

	db.ItemIcons = core.FilterMap(db.ItemIcons, func(_ int32, icon *proto.IconData) bool {
		return icon.Name != "" && icon.Icon != ""
	})
	db.SpellIcons = core.FilterMap(db.SpellIcons, func(_ int32, icon *proto.IconData) bool {
		return icon.Name != "" && icon.Icon != ""
	})
}

// AttachFactionInformation attaches faction information (faction restrictions) to the DB items.
// func AttachFactionInformation(db *database.WowDatabase, factionRestrictions map[int32]proto.UIItem_FactionRestriction) {
// 	for _, item := range db.Items {
// 		item.FactionRestriction = factionRestrictions[item.Id]
// 	}
// }

// Filters out entities which shouldn't be included in the sim.
func ApplySimmableFilters(db *database.WowDatabase) {
	db.Items = core.FilterMap(db.Items, simmableItemFilter)
}
func ApplyNonSimmableFilters(db *database.WowDatabase) {
	db.Items = core.FilterMap(db.Items, func(id int32, item *proto.UIItem) bool {
		return !simmableItemFilter(id, item)
	})
}
func simmableItemFilter(_ int32, item *proto.UIItem) bool {
	if _, ok := database.ItemAllowList[item.Id]; ok {
		return true
	}

	if item.Quality < proto.ItemQuality_ItemQualityUncommon {
		return false
	} else if item.Quality == proto.ItemQuality_ItemQualityArtifact {
		return false
	} else if item.Quality > proto.ItemQuality_ItemQualityHeirloom {
		return false
	} else if item.Quality < proto.ItemQuality_ItemQualityEpic {
		if item.Ilvl < 10 {
			return false
		}
		if item.Ilvl < 10 && item.SetName == "" {
			return false
		}
	} else {
		// Epic and legendary items might come from classic, so use a lower ilvl threshold.
		if item.Quality != proto.ItemQuality_ItemQualityHeirloom && item.Ilvl < 10 {
			return false
		}
	}
	if item.Ilvl == 0 {
		fmt.Printf("Missing ilvl: %s\n", item.Name)
	}

	return true
}

type TalentConfig struct {
	FieldName string `json:"fieldName"`
	// Spell ID for each rank of this talent.
	// Omitted ranks will be inferred by incrementing from the last provided rank.
	SpellIds  []int32 `json:"spellIds"`
	MaxPoints int32   `json:"maxPoints"`
}

type TalentTreeConfig struct {
	Name          string         `json:"name"`
	BackgroundUrl string         `json:"backgroundUrl"`
	Talents       []TalentConfig `json:"talents"`
}

func getSpellIdsFromTalentJson(infile *string) []int32 {
	data, err := os.ReadFile(*infile)
	if err != nil {
		log.Fatalf("failed to load talent json file: %s", err)
	}

	var buf bytes.Buffer
	err = json.Compact(&buf, []byte(data))
	if err != nil {
		log.Fatalf("failed to compact json: %s", err)
	}

	var talents []TalentTreeConfig

	err = json.Unmarshal(buf.Bytes(), &talents)
	if err != nil {
		log.Fatalf("failed to parse talent to json %s", err)
	}

	spellIds := make([]int32, 0)

	for _, tree := range talents {
		for _, talent := range tree.Talents {
			spellIds = append(spellIds, talent.SpellIds...)

			// Infer omitted spell IDs.
			if len(talent.SpellIds) < int(talent.MaxPoints) {
				curSpellId := talent.SpellIds[len(talent.SpellIds)-1]
				for i := len(talent.SpellIds); i < int(talent.MaxPoints); i++ {
					curSpellId++
					spellIds = append(spellIds, curSpellId)
				}
			}
		}
	}

	return spellIds
}

func GetAllTalentSpellIds(inputsDir *string) map[string][]int32 {
	talentsDir := fmt.Sprintf("%s/../../ui/core/talents/trees", *inputsDir)
	specFiles := []string{
		"druid.json",
		"hunter.json",
		"mage.json",
		"paladin.json",
		"priest.json",
		"rogue.json",
		"shaman.json",
		"warlock.json",
		"warrior.json",
	}

	ret_db := make(map[string][]int32, 0)

	for _, specFile := range specFiles {
		specPath := fmt.Sprintf("%s/%s", talentsDir, specFile)
		ret_db[specFile[:len(specFile)-5]] = getSpellIdsFromTalentJson(&specPath)
	}

	return ret_db

}

func CreateTempAgent(r *proto.Raid) core.Agent {
	encounter := core.MakeSingleTargetEncounter(60, 0.0)
	env, _, _ := core.NewEnvironment(r, encounter, false)
	return env.Raid.Parties[0].Players[0]
}

type RotContainer struct {
	Name string
	Raid *proto.Raid
}

func GetAllRotationSpellIds() map[string][]int32 {
	sim.RegisterAll()

	rotMapping := []RotContainer{
		{Name: "feral", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassDruid,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_FeralDruid{FeralDruid: &proto.FeralDruid{Options: &proto.FeralDruid_Options{}, Rotation: &proto.FeralDruid_Rotation{}}}), nil, nil, nil)},
		{Name: "balance", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassDruid,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_BalanceDruid{BalanceDruid: &proto.BalanceDruid{Options: &proto.BalanceDruid_Options{}}}), nil, nil, nil)},
		// TODO: Druid Tank Sim
		// {Name: "druid tank", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
		// 	Class:     proto.Class_ClassDruid,
		// 	Level:     60,
		// 	Equipment: &proto.EquipmentSpec{},
		// }, &proto.Player_FeralTankDruid{FeralTankDruid: &proto.FeralTankDruid{Options: &proto.FeralTankDruid_Options{}}}), nil, nil, nil)},
		{Name: "elemental", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassShaman,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_ElementalShaman{ElementalShaman: &proto.ElementalShaman{Options: &proto.ElementalShaman_Options{}}}), nil, nil, nil)},
		{Name: "enhance", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassShaman,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_EnhancementShaman{EnhancementShaman: &proto.EnhancementShaman{Options: &proto.EnhancementShaman_Options{}}}), nil, nil, nil)},
		{Name: "hunter", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassHunter,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_Hunter{Hunter: &proto.Hunter{Options: &proto.Hunter_Options{}}}), nil, nil, nil)},
		{Name: "mage", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassMage,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_Mage{Mage: &proto.Mage{Options: &proto.Mage_Options{}}}), nil, nil, nil)},
		{Name: "shadow", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassPriest,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_ShadowPriest{ShadowPriest: &proto.ShadowPriest{Options: &proto.ShadowPriest_Options{}}}), nil, nil, nil)},
		{Name: "rogue", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassRogue,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
			Rotation:  &proto.APLRotation{},
		}, &proto.Player_Rogue{Rogue: &proto.Rogue{}}), nil, nil, nil)},
		// TODO: Rogue Tank Sim
		// {Name: "tank rogue", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
		// 	Class:     proto.Class_ClassRogue,
		// 	Level:     60,
		// 	Equipment: &proto.EquipmentSpec{},
		// 	Rotation:  &proto.APLRotation{},
		// }, &proto.Player_TankRogue{TankRogue: &proto.TankRogue{}}), nil, nil, nil)},
		{Name: "warrior", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassWarrior,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_Warrior{Warrior: &proto.Warrior{Options: &proto.Warrior_Options{}}}), nil, nil, nil)},
		// TODO: Warrior Tank Sim
		// {Name: "warrior tank", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
		// 	Class:     proto.Class_ClassWarrior,
		// 	Level:     60,
		// 	Equipment: &proto.EquipmentSpec{},
		// }, &proto.Player_ProtectionWarrior{ProtectionWarrior: &proto.ProtectionWarrior{Options: &proto.ProtectionWarrior_Options{}}}), nil, nil, nil)},
		{Name: "ret", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassPaladin,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_RetributionPaladin{RetributionPaladin: &proto.RetributionPaladin{Options: &proto.RetributionPaladin_Options{}}}), nil, nil, nil)},
		{Name: "warlock", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassWarlock,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_Warlock{Warlock: &proto.Warlock{Options: &proto.WarlockOptions{}}}), nil, nil, nil)},
		{Name: "tank warlock", Raid: core.SinglePlayerRaidProto(core.WithSpec(&proto.Player{
			Class:     proto.Class_ClassWarlock,
			Level:     60,
			Equipment: &proto.EquipmentSpec{},
		}, &proto.Player_TankWarlock{TankWarlock: &proto.TankWarlock{Options: &proto.WarlockOptions{}}}), nil, nil, nil)},
	}

	ret_db := make(map[string][]int32, 0)

	for _, r := range rotMapping {
		f := CreateTempAgent(r.Raid).GetCharacter()

		spells := make([]int32, 0, len(f.Spellbook))

		for _, s := range f.Spellbook {
			if s.SpellID != 0 {
				spells = append(spells, s.SpellID)
			}
		}

		for _, s := range f.GetAuras() {
			if s.ActionID.SpellID != 0 {
				spells = append(spells, s.ActionID.SpellID)
			}
		}

		ret_db[r.Name] = spells
	}
	return ret_db
}
