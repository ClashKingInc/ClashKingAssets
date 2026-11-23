import json
import re
from pathlib import Path

SOUND_INDEX = Path("assets/static_data/sfx.json")
SOUND_PATH = re.compile(r"sfx/[^/\\\x00-\x1f\x7f]+\.ogg", re.IGNORECASE)

# Reviewed sounds shared by the game interface, tutorials, and other common actions.
GENERAL_SOUNDS = frozenset({
    "sfx/activate_your_ring_sfx_01.ogg",
    "sfx/air_switch_guardian_01.ogg",
    "sfx/alliance_jingle_v2.ogg",
    "sfx/battle_scene_change_sfx_01.ogg",
    "sfx/bb_scene_change_02.ogg",
    "sfx/BbStarBonus_Collect_SFX.ogg",
    "sfx/BbStarBonus_Popup_SFX.ogg",
    "sfx/BbStarBonus_StarAppear_SFX.ogg",
    "sfx/BbStarBonus_StarHit_SFX.ogg",
    "sfx/building_construct_07.ogg",
    "sfx/building_finished_01.ogg",
    "sfx/button_click.ogg",
    "sfx/buy_event_pass_01.ogg",
    "sfx/buy_gold_pass_01.ogg",
    "sfx/cap_build_part_in_01.ogg",
    "sfx/capital_customized_01.ogg",
    "sfx/capital_rep_up_01.ogg",
    "sfx/challenge_ount_01.ogg",
    "sfx/change_scenery_sfx_01.ogg",
    "sfx/clan_task_failed_01.ogg",
    "sfx/clanwars_engaged_on_map_01small.ogg",
    "sfx/clash_get_first_star_01.ogg",
    "sfx/clash_get_second_star_01.ogg",
    "sfx/clash_get_star_fly_ui_01.ogg",
    "sfx/clash_get_third_star_01.ogg",
    "sfx/clash_grass_pop_01.ogg",
    "sfx/coin_steal_02.ogg",
    "sfx/collect_capital_gold_01.ogg",
    "sfx/collect_clan_reward_01.ogg",
    "sfx/complete_challenge_01.ogg",
    "sfx/complete_clan_task_01.ogg",
    "sfx/copy_01.ogg",
    "sfx/dark_elixir_collect.ogg",
    "sfx/dark_elixir_steal.ogg",
    "sfx/donate_button_01.ogg",
    "sfx/duel_notification_01.ogg",
    "sfx/elixir_collect_02.ogg",
    "sfx/ElixirCart_Collect_SFX.ogg",
    "sfx/ElixirCart_Open_SFX.ogg",
    "sfx/ElixirCart_OpenRewards_SFX.ogg",
    "sfx/find_clan_01.ogg",
    "sfx/gem_boost_01.ogg",
    "sfx/goals_btn_01.ogg",
    "sfx/grandvillager_murmur_04.ogg",
    "sfx/grandvillager_murmur_vo_10.ogg",
    "sfx/grandvillager_murmur_vo_hmm_01.ogg",
    "sfx/ground_switch_guardian_01.ogg",
    "sfx/hero_energized.ogg",
    "sfx/hero_hit_claw_gen_01.ogg",
    "sfx/hero_hit_shield_gen_01.ogg",
    "sfx/hero_hit_shield_gen_02.ogg",
    "sfx/hero_patrol.ogg",
    "sfx/hero_sleep.ogg",
    "sfx/hero_spot_01.ogg",
    "sfx/hero_ui_select_01.ogg",
    "sfx/hero_upgrade_02.ogg",
    "sfx/league_bonus.ogg",
    "sfx/league_demote.ogg",
    "sfx/league_promote.ogg",
    "sfx/leg_league_01.ogg",
    "sfx/leg_league_over_01.ogg",
    "sfx/loading_screen_jingle.ogg",
    "sfx/merge_magic_01.ogg",
    "sfx/moving_07.ogg",
    "sfx/new_raid.ogg",
    "sfx/no_troops_01short.ogg",
    "sfx/open_clan_games_01.ogg",
    "sfx/purchase_complete.ogg",
    "sfx/RandomReward_Chest_Ambience.ogg",
    "sfx/RandomReward_Chest_Appear.ogg",
    "sfx/RandomReward_Chest_Find_Negative.ogg",
    "sfx/RandomReward_Chest_Find_Positive.ogg",
    "sfx/RandomReward_Chest_Hammer_01.ogg",
    "sfx/RandomReward_Chest_Hammer_02.ogg",
    "sfx/RandomReward_Chest_Hammer_03.ogg",
    "sfx/RandomReward_Chest_Hammer_04.ogg",
    "sfx/RandomReward_Chest_Rattle.ogg",
    "sfx/RandomReward_Chest_RewardReveal.ogg",
    "sfx/RandomReward_Chest_Upgrade.ogg",
    "sfx/rearm_jingle_01.ogg",
    "sfx/road_of_goals_flow_01.ogg",
    "sfx/select_balloon_bus.ogg",
    "sfx/select_forge.ogg",
    "sfx/select_trader.ogg",
    "sfx/sell_item_01.ogg",
    "sfx/sfx_ui_cardcollects_appear.ogg",
    "sfx/sfx_ui_cardcollects_fly_01.ogg",
    "sfx/sfx_ui_cardcollects_fly_02.ogg",
    "sfx/sfx_ui_cardcollects_fly_03.ogg",
    "sfx/sfx_ui_cardcollects_rewardreveal_1_com.ogg",
    "sfx/sfx_ui_cardcollects_rewardreveal_2_uncom.ogg",
    "sfx/sfx_ui_cardcollects_rewardreveal_3_rare.ogg",
    "sfx/sfx_ui_cardcollects_tap_01.ogg",
    "sfx/sfx_ui_cardcollects_tap_02.ogg",
    "sfx/sfx_ui_cardcollects_tap_03.ogg",
    "sfx/sfx_ui_league_screen_demotion.ogg",
    "sfx/sfx_ui_league_screen_legendary.ogg",
    "sfx/sfx_ui_league_screen_promotion.ogg",
    "sfx/sfx_ui_league_screen_remain.ogg",
    "sfx/sfx_ui_randomreward_chest_ore_ambience.ogg",
    "sfx/sfx_ui_randomreward_chest_ore_hammer.ogg",
    "sfx/spell_ready_01.ogg",
    "sfx/spell_ui_select_01.ogg",
    "sfx/start_troop_upgrade_01.ogg",
    "sfx/stun_gen.ogg",
    "sfx/tap_boat_01.ogg",
    "sfx/tap_clan_horse_01.ogg",
    "sfx/tap_own_captial_house_01.ogg",
    "sfx/town_hall_upgrade_jingle_01.ogg",
    "sfx/training_finished_03.ogg",
    "sfx/troop_ui_select_01.ogg",
    "sfx/troop_upgrade_finished_01.ogg",
    "sfx/tut_builder_backtosleep_01.ogg",
    "sfx/tut_builder_neutral_01.ogg",
    "sfx/tut_builder_snore_01.ogg",
    "sfx/tut_builder_surprised_01.ogg",
    "sfx/tut_builder_wakeup_01.ogg",
    "sfx/tutor_happy_01.ogg",
    "sfx/tutor_hey_01.ogg",
    "sfx/tutor_no_talk_woop_01.ogg",
    "sfx/tutor_reenage_01.ogg",
    "sfx/tutor_reenage_03.ogg",
    "sfx/tutor_reenage_07.ogg",
    "sfx/tutor_reenage_08.ogg",
    "sfx/tutor_reenage_09.ogg",
    "sfx/tutor_reenage_10.ogg",
    "sfx/tutor_relief_01.ogg",
    "sfx/tutor_talk_aha_01.ogg",
    "sfx/tutor_talk_ahoy_01.ogg",
    "sfx/tutor_talk_yeah_01.ogg",
    "sfx/tutor_urgency_01.ogg",
    "sfx/tutor_urgency_02.ogg",
    "sfx/tutor_urgency_04.ogg",
    "sfx/tutor_urgency_05.ogg",
    "sfx/tutor_wow_01.ogg",
    "sfx/UI_GoldRush_Bar_Increase_Lrg.ogg",
    "sfx/UI_GoldRush_Bar_Increase_Med.ogg",
    "sfx/UI_GoldRush_Bar_Increase_Sml.ogg",
    "sfx/UI_GoldRush_ButtonTap.ogg",
    "sfx/UI_GoldRush_EnterScreen.ogg",
    "sfx/UI_GoldRush_Item_Final_Claim.ogg",
    "sfx/UI_GoldRush_Item_Final_Unlock.ogg",
    "sfx/UI_GoldRush_Item_Gen_Claim.ogg",
    "sfx/UI_GoldRush_Item_Gen_Unlock.ogg",
    "sfx/UI_GoldRush_Nuggets_Local_Fly.ogg",
    "sfx/UI_GoldRush_Nuggets_Local_Land.ogg",
    "sfx/UI_GoldRush_Nuggets_Remote_Fly.ogg",
    "sfx/ui_sp_background_lp.ogg",
    "sfx/ui_sp_bank_break.ogg",
    "sfx/ui_sp_bank_filling.ogg",
    "sfx/ui_sp_bank_shake.ogg",
    "sfx/ui_sp_complete.ogg",
    "sfx/ui_sp_enter.ogg",
    "sfx/ui_sp_exit.ogg",
    "sfx/ui_sp_progress_large_lp.ogg",
    "sfx/ui_sp_progress_reach.ogg",
    "sfx/ui_sp_progress_small.ogg",
    "sfx/ui_sp_reward_chosen.ogg",
    "sfx/ui_sp_unlock.ogg",
    "sfx/ui_streakevent_amb_wind_loop.ogg",
    "sfx/ui_streakevent_chest_open.ogg",
    "sfx/ui_streakevent_streak_complete.ogg",
    "sfx/ui_streakevent_streak_failed.ogg",
    "sfx/ui_streakevent_torch_extinguish.ogg",
    "sfx/ui_streakevent_torch_ignite.ogg",
    "sfx/ui_streakevent_wagon_break.ogg",
    "sfx/ui_streakevent_wagon_move.ogg",
    "sfx/ui_streakevent_wagon_repair.ogg",
    "sfx/villager_onboard_01.ogg",
    "sfx/VO_Giant_Announcer_Happy_01.ogg",
    "sfx/VO_Giant_Announcer_Happy_02.ogg",
    "sfx/VO_Giant_Announcer_Happy_03.ogg",
    "sfx/VO_Giant_Announcer_Neutral_01.ogg",
    "sfx/VO_Giant_Announcer_Neutral_02.ogg",
    "sfx/VO_Giant_Announcer_Neutral_03.ogg",
    "sfx/VO_Villager_Announcer_Happy_01.ogg",
    "sfx/VO_Villager_Announcer_Happy_02.ogg",
    "sfx/VO_Villager_Announcer_Happy_03.ogg",
    "sfx/VO_Villager_Announcer_Scared_01.ogg",
    "sfx/VO_Villager_Announcer_Scared_02.ogg",
    "sfx/VO_Villager_Announcer_VeryHappy_01.ogg",
    "sfx/VO_Villager_Announcer_VeryHappy_02.ogg",
    "sfx/VO_Villager_Announcer_VeryHappy_03.ogg",
    "sfx/VO_Wizard_Metal_Angry_01.ogg",
    "sfx/VO_Wizard_Metal_Angry_02.ogg",
    "sfx/VO_Wizard_Metal_Happy_01.ogg",
    "sfx/VO_Wizard_Metal_Happy_02.ogg",
    "sfx/VO_Wizard_Metal_Happy_03.ogg",
    "sfx/VO_Wizard_Metal_Neutral_01.ogg",
    "sfx/VO_Wizard_Metal_Neutral_02.ogg",
    "sfx/VO_Wizard_Metal_Neutral_03.ogg",
    "sfx/VO_Wizard_Metal_Scared_01.ogg",
    "sfx/VO_Wizard_Metal_Scared_02.ogg",
    "sfx/xp_gain_06.ogg",
})


def load_ignored_ids(path: Path = Path(".ignored.txt")) -> set[int]:
    if not path.exists():
        return set()

    ignored_ids = set()
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        value = line.partition("#")[0].strip()
        if not value:
            continue
        try:
            ignored_ids.add(int(value))
        except ValueError as exc:
            raise ValueError(f"invalid ID in {path} on line {line_number}: {value!r}") from exc
    return ignored_ids


def eligible_sound_owners(data: dict, ignored: set[int]) -> set[str]:
    return {
        f"{kind}:{entry['_id']}"
        for kind, entries in data.items()
        if isinstance(entries, list)
        for entry in entries
        if isinstance(entry, dict) and isinstance(entry.get("_id"), int) and entry["_id"] not in ignored
    }


def load_sound_index(path: Path = SOUND_INDEX) -> dict[str, list[str]]:
    if path.is_file():
        return json.loads(path.read_text(encoding="utf-8"))
    return {}


def general_sound_paths() -> set[str]:
    return set(GENERAL_SOUNDS)


def check_sound_effects(root: Path) -> None:
    mapping = load_sound_index(root / "static_data/sfx.json")
    item_paths = {path for paths in mapping.values() for path in paths}
    allowed = item_paths | general_sound_paths()
    for path in sorted(allowed):
        if not SOUND_PATH.fullmatch(path):
            raise ValueError(f"invalid shared sound path: {path!r}")

    if mapping:
        data = json.loads((root / "static_data.json").read_text(encoding="utf-8"))
        if blocked := mapping.keys() - eligible_sound_owners(data, load_ignored_ids()):
            raise ValueError("sound effects map to ignored or missing items: " + ", ".join(sorted(blocked)))

    files = {
        path.relative_to(root).as_posix()
        for folder in root.rglob("sfx")
        if folder.is_dir()
        for path in folder.rglob("*")
        if path.is_file()
    }
    if blocked := files - allowed:
        raise ValueError("sound effects lack an eligible owner or approved general use: " + ", ".join(sorted(blocked)))
    if missing := item_paths - files:
        raise ValueError("item sound files are missing: " + ", ".join(sorted(missing)))
