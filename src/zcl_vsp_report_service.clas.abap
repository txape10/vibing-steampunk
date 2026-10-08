CLASS zcl_vsp_report_service DEFINITION
  PUBLIC
  FINAL
  CREATE PUBLIC.

  PUBLIC SECTION.
    INTERFACES zif_vsp_service.

  PRIVATE SECTION.
    TYPES:
      BEGIN OF ty_get_request,
        class_name  TYPE string,
        method_name TYPE string,
        language    TYPE string,
      END OF ty_get_request,

      BEGIN OF ty_set_request,
        class_name     TYPE string,
        method_name    TYPE string,
        parameter_name TYPE string,
        description    TYPE string,
        language       TYPE string,
        clear_text     TYPE abap_bool,
        transport      TYPE string,
      END OF ty_set_request,

      BEGIN OF ty_param_text,
        name        TYPE string,
        decl_type   TYPE string,
        version     TYPE string,
        description TYPE string,
        has_text    TYPE abap_bool,
      END OF ty_param_text,
      ty_param_texts TYPE STANDARD TABLE OF ty_param_text WITH EMPTY KEY,

      BEGIN OF ty_method_texts,
        method_name TYPE string,
        cmp_type    TYPE string,
        params      TYPE ty_param_texts,
      END OF ty_method_texts,
      ty_method_texts_tab TYPE STANDARD TABLE OF ty_method_texts WITH EMPTY KEY,

      BEGIN OF ty_get_response,
        class_name      TYPE string,
        package_name    TYPE string,
        master_language TYPE string,
        language        TYPE string,
        method_list     TYPE ty_method_texts_tab,
      END OF ty_get_response,

      BEGIN OF ty_set_response,
        status          TYPE string,
        class_name      TYPE string,
        method_name     TYPE string,
        parameter_name  TYPE string,
        language        TYPE string,
        master_language TYPE string,
        old_description TYPE string,
        new_description TYPE string,
        row_state       TYPE string,
        verified        TYPE abap_bool,
        transport       TYPE string,
        recording       TYPE string,
      END OF ty_set_response,

      BEGIN OF ty_error,
        code    TYPE string,
        message TYPE string,
      END OF ty_error,

      BEGIN OF ty_param_key,
        clsname TYPE seosubco-clsname,
        cmpname TYPE seosubco-cmpname,
        sconame TYPE seosubco-sconame,
      END OF ty_param_key.

    CONSTANTS gc_trfunction_workbench TYPE e070-trfunction VALUE 'K'.
    CONSTANTS gc_trstatus_modifiable  TYPE e070-trstatus VALUE 'D'.
    CONSTANTS gc_version_active       TYPE seosubcodf-version VALUE '1'.
    CONSTANTS gc_version_inactive     TYPE seosubcodf-version VALUE '0'.
    CONSTANTS gc_scotype_param        TYPE seosubco-scotype VALUE '0'.

    METHODS handle_get_class_param_texts
      IMPORTING is_message         TYPE zif_vsp_service=>ty_message
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS handle_set_class_param_text
      IMPORTING is_message         TYPE zif_vsp_service=>ty_message
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS validate_set_request
      IMPORTING is_req          TYPE ty_set_request
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS locate_target
      IMPORTING is_req        TYPE ty_set_request
      EXPORTING es_error      TYPE ty_error
                ev_lang       TYPE sy-langu
                ev_masterlang TYPE tadir-masterlang
                ev_devclass   TYPE tadir-devclass.

    METHODS validate_transport
      IMPORTING is_req          TYPE ty_set_request
                iv_devclass     TYPE tadir-devclass
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS check_transport_given
      IMPORTING is_req          TYPE ty_set_request
                iv_devclass     TYPE tadir-devclass
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS read_request_header
      IMPORTING iv_request TYPE trkorr
      EXPORTING es_header  TYPE e070
                es_error   TYPE ty_error.

    METHODS check_request_ownership
      IMPORTING iv_request      TYPE trkorr
                is_header       TYPE e070
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS has_own_modifiable_task
      IMPORTING iv_request         TYPE trkorr
      RETURNING VALUE(rv_has_task) TYPE abap_bool.

    METHODS check_class_lock
      IMPORTING iv_request      TYPE trkorr
                iv_class        TYPE string
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS request_of_lock
      IMPORTING iv_trkorr         TYPE trkorr
      RETURNING VALUE(rv_request) TYPE trkorr.

    METHODS check_class_original
      IMPORTING iv_class        TYPE string
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS record_in_request
      IMPORTING iv_request      TYPE trkorr
                iv_class        TYPE string
      EXPORTING ev_no_recording TYPE abap_bool
                ev_error        TYPE string.

    METHODS apply_set_request
      IMPORTING iv_id              TYPE string
                is_req             TYPE ty_set_request
                iv_lang            TYPE sy-langu
                iv_masterlang      TYPE tadir-masterlang
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS build_param_key
      IMPORTING is_req        TYPE ty_set_request
      RETURNING VALUE(rs_key) TYPE ty_param_key.

    METHODS read_param_rows
      IMPORTING is_key   TYPE ty_param_key
                is_req   TYPE ty_set_request
      EXPORTING es_co    TYPE seosubco
                es_df    TYPE seosubcodf
                es_error TYPE ty_error.

    METHODS describe_missing_param
      IMPORTING is_key          TYPE ty_param_key
                is_req          TYPE ty_set_request
      RETURNING VALUE(rs_error) TYPE ty_error.

    METHODS read_param_text
      IMPORTING is_key    TYPE ty_param_key
                iv_lang   TYPE sy-langu
      EXPORTING ev_text   TYPE seosubcotx-descript
                ev_exists TYPE abap_bool.

    METHODS row_state
      IMPORTING iv_exists       TYPE abap_bool
                iv_text         TYPE seosubcotx-descript
      RETURNING VALUE(rv_state) TYPE string.

    METHODS record_change
      IMPORTING is_req    TYPE ty_set_request
      EXPORTING es_error  TYPE ty_error
      CHANGING  cs_result TYPE ty_set_response.

    METHODS verify_written_text
      IMPORTING is_key    TYPE ty_param_key
                is_req    TYPE ty_set_request
                iv_lang   TYPE sy-langu
      EXPORTING es_error  TYPE ty_error
      CHANGING  cs_result TYPE ty_set_response.

    METHODS modify_param_text
      IMPORTING is_co           TYPE seosubco
                is_df           TYPE seosubcodf
                iv_lang         TYPE sy-langu
                iv_description  TYPE string
      RETURNING VALUE(rv_error) TYPE string.

    METHODS resolve_class
      IMPORTING iv_class      TYPE string
      EXPORTING ev_found      TYPE abap_bool
                ev_devclass   TYPE tadir-devclass
                ev_masterlang TYPE tadir-masterlang.

    METHODS resolve_language
      IMPORTING iv_language TYPE string
                iv_default  TYPE sy-langu
      EXPORTING ev_lang     TYPE sy-langu
                ev_ok       TYPE abap_bool.

    METHODS set_result
      IMPORTING iv_id              TYPE string
                is_result          TYPE ty_set_response
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS handle_run_report
      IMPORTING is_message         TYPE zif_vsp_service=>ty_message
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS handle_get_text_elements
      IMPORTING is_message         TYPE zif_vsp_service=>ty_message
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS handle_set_text_elements
      IMPORTING is_message         TYPE zif_vsp_service=>ty_message
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS handle_get_variants
      IMPORTING is_message         TYPE zif_vsp_service=>ty_message
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

    METHODS extract_param
      IMPORTING iv_params       TYPE string
                iv_name         TYPE string
      RETURNING VALUE(rv_value) TYPE string.

    METHODS extract_param_object
      IMPORTING iv_params       TYPE string
                iv_name         TYPE string
      RETURNING VALUE(rv_json)  TYPE string.

    METHODS escape_json
      IMPORTING iv_string         TYPE string
      RETURNING VALUE(rv_escaped) TYPE string.

    METHODS build_error
      IMPORTING iv_id              TYPE string
                iv_code            TYPE string
                iv_message         TYPE string
      RETURNING VALUE(rs_response) TYPE zif_vsp_service=>ty_response.

ENDCLASS.


CLASS zcl_vsp_report_service IMPLEMENTATION.

  METHOD zif_vsp_service~get_domain.
    rv_domain = 'report'.
  ENDMETHOD.

  METHOD zif_vsp_service~handle_message.
    CASE is_message-action.
      WHEN 'runReport'.
        rs_response = handle_run_report( is_message ).
      WHEN 'getTextElements'.
        rs_response = handle_get_text_elements( is_message ).
      WHEN 'setTextElements'.
        rs_response = handle_set_text_elements( is_message ).
      WHEN 'getVariants'.
        rs_response = handle_get_variants( is_message ).
      WHEN 'getClassParamTexts'.
        rs_response = handle_get_class_param_texts( is_message ).
      WHEN 'setClassParamText'.
        rs_response = handle_set_class_param_text( is_message ).
      WHEN OTHERS.
        rs_response = build_error(
          iv_id      = is_message-id
          iv_code    = 'UNKNOWN_ACTION'
          iv_message = |Action '{ is_message-action }' not supported|
        ).
    ENDCASE.
  ENDMETHOD.

  METHOD zif_vsp_service~on_disconnect.
  ENDMETHOD.

  METHOD handle_run_report.
    DATA: lt_rsparams  TYPE TABLE OF rsparams,
          lr_data      TYPE REF TO data,
          lv_start     TYPE timestampl,
          lv_end       TYPE timestampl,
          lv_runtime   TYPE i,
          lv_report    TYPE progname,
          lv_variant   TYPE variant.

    DATA(lv_report_str) = extract_param( iv_params = is_message-params iv_name = 'report' ).
    DATA(lv_variant_str) = extract_param( iv_params = is_message-params iv_name = 'variant' ).
    DATA(lv_capture) = extract_param( iv_params = is_message-params iv_name = 'capture_alv' ).
    DATA(lv_max_str) = extract_param( iv_params = is_message-params iv_name = 'max_rows' ).
    DATA(lv_params_json) = extract_param_object( iv_params = is_message-params iv_name = 'params' ).

    IF lv_report_str IS INITIAL.
      rs_response = build_error( iv_id = is_message-id iv_code = 'MISSING_PARAM' iv_message = 'Parameter report is required' ).
      RETURN.
    ENDIF.

    TRANSLATE lv_report_str TO UPPER CASE.
    lv_report = lv_report_str.
    IF lv_variant_str IS NOT INITIAL.
      TRANSLATE lv_variant_str TO UPPER CASE.
      lv_variant = lv_variant_str.
    ENDIF.

    DATA(lv_capture_alv) = COND abap_bool( WHEN lv_capture = 'false' THEN abap_false ELSE abap_true ).
    DATA(lv_max_rows) = COND i( WHEN lv_max_str IS NOT INITIAL THEN CONV i( lv_max_str ) ELSE 1000 ).

    SELECT SINGLE name FROM trdir INTO @DATA(lv_exists) WHERE name = @lv_report.
    IF sy-subrc <> 0.
      rs_response = build_error( iv_id = is_message-id iv_code = 'REPORT_NOT_FOUND' iv_message = |Report { lv_report } not found| ).
      RETURN.
    ENDIF.

    IF lv_params_json IS NOT INITIAL.
      DATA(lv_work) = lv_params_json.
      WHILE lv_work CS '"'.
        DATA lv_pname TYPE string.
        DATA lv_pval TYPE string.
        FIND PCRE '"([^"]+)"\s*:\s*"([^"]*)"' IN lv_work SUBMATCHES lv_pname lv_pval.
        IF sy-subrc = 0.
          TRANSLATE lv_pname TO UPPER CASE.
          DATA lv_selname TYPE rsscr_name.
          lv_selname = lv_pname.
          APPEND VALUE rsparams(
            selname = lv_selname
            kind    = 'P'
            sign    = 'I'
            option  = 'EQ'
            low     = lv_pval
          ) TO lt_rsparams.
          FIND FIRST OCCURRENCE OF |"{ lv_pname }"| IN lv_work MATCH OFFSET DATA(lv_off) MATCH LENGTH DATA(lv_len) IGNORING CASE.
          IF sy-subrc = 0 AND strlen( lv_work ) > lv_off + lv_len.
            lv_work = lv_work+lv_off.
            lv_work = lv_work+lv_len.
          ELSE.
            EXIT.
          ENDIF.
        ELSE.
          EXIT.
        ENDIF.
      ENDWHILE.
    ENDIF.

    GET TIME STAMP FIELD lv_start.

    DATA(lv_o) = '{'.
    DATA(lv_c) = '}'.
    DATA lv_json TYPE string.
    DATA lv_alv_json TYPE string.
    DATA lv_columns_json TYPE string.
    DATA lv_total_rows TYPE i.
    DATA lv_truncated TYPE abap_bool.

    TRY.
        IF lv_capture_alv = abap_true.
          cl_salv_bs_runtime_info=>set(
            display  = abap_false
            metadata = abap_true
            data     = abap_true ).
        ENDIF.

        IF lv_variant IS NOT INITIAL.
          SUBMIT (lv_report)
            USING SELECTION-SET lv_variant
            AND RETURN.
        ELSEIF lt_rsparams IS NOT INITIAL.
          SUBMIT (lv_report)
            WITH SELECTION-TABLE lt_rsparams
            AND RETURN.
        ELSE.
          SUBMIT (lv_report) AND RETURN.
        ENDIF.

        IF lv_capture_alv = abap_true.
          TRY.
              cl_salv_bs_runtime_info=>get_data_ref( IMPORTING r_data = lr_data ).

              IF lr_data IS BOUND.
                FIELD-SYMBOLS <lt_data> TYPE ANY TABLE.
                ASSIGN lr_data->* TO <lt_data>.
                lv_total_rows = lines( <lt_data> ).

                DATA(lo_type) = cl_abap_typedescr=>describe_by_data_ref( lr_data ).
                IF lo_type->kind = cl_abap_typedescr=>kind_table.
                  DATA(lo_table) = CAST cl_abap_tabledescr( lo_type ).
                  DATA(lo_struct) = CAST cl_abap_structdescr( lo_table->get_table_line_type( ) ).
                  lv_columns_json = '['.
                  DATA lv_col_first TYPE abap_bool VALUE abap_true.
                  LOOP AT lo_struct->components INTO DATA(ls_comp).
                    IF lv_col_first = abap_false.
                      lv_columns_json = |{ lv_columns_json },|.
                    ENDIF.
                    lv_columns_json = |{ lv_columns_json }{ lv_o }"name":"{ ls_comp-name }","type":"{ ls_comp-type_kind }"{ lv_c }|.
                    lv_col_first = abap_false.
                  ENDLOOP.
                  lv_columns_json = |{ lv_columns_json }]|.

                  lv_alv_json = '['.
                  DATA lv_row_first TYPE abap_bool VALUE abap_true.
                  DATA lv_row_count TYPE i.
                  LOOP AT <lt_data> ASSIGNING FIELD-SYMBOL(<ls_row>).
                    lv_row_count = lv_row_count + 1.
                    IF lv_row_count > lv_max_rows.
                      lv_truncated = abap_true.
                      EXIT.
                    ENDIF.
                    IF lv_row_first = abap_false.
                      lv_alv_json = |{ lv_alv_json },|.
                    ENDIF.
                    lv_alv_json = |{ lv_alv_json }{ lv_o }|.
                    DATA lv_fld_first TYPE abap_bool VALUE abap_true.
                    LOOP AT lo_struct->components INTO ls_comp.
                      IF lv_fld_first = abap_false.
                        lv_alv_json = |{ lv_alv_json },|.
                      ENDIF.
                      ASSIGN COMPONENT ls_comp-name OF STRUCTURE <ls_row> TO FIELD-SYMBOL(<fv>).
                      IF sy-subrc = 0.
                        DATA lv_val TYPE string.
                        TRY.
                            lv_val = <fv>.
                          CATCH cx_root.
                            lv_val = ''.
                        ENDTRY.
                        lv_alv_json = |{ lv_alv_json }"{ ls_comp-name }":"{ escape_json( lv_val ) }"|.
                      ENDIF.
                      lv_fld_first = abap_false.
                    ENDLOOP.
                    lv_alv_json = |{ lv_alv_json }{ lv_c }|.
                    lv_row_first = abap_false.
                  ENDLOOP.
                  lv_alv_json = |{ lv_alv_json }]|.
                ENDIF.
              ENDIF.
            CATCH cx_salv_bs_sc_runtime_info.
          ENDTRY.
        ENDIF.

      CATCH cx_root INTO DATA(lx_error).
        IF lv_capture_alv = abap_true.
          cl_salv_bs_runtime_info=>clear_all( ).
        ENDIF.
        rs_response = build_error( iv_id = is_message-id iv_code = 'SUBMIT_ERROR' iv_message = lx_error->get_text( ) ).
        RETURN.
    ENDTRY.

    IF lv_capture_alv = abap_true.
      cl_salv_bs_runtime_info=>clear_all( ).
    ENDIF.

    GET TIME STAMP FIELD lv_end.
    lv_runtime = cl_abap_tstmp=>subtract( tstmp1 = lv_end tstmp2 = lv_start ) * 1000.

    DATA(lv_alv_captured) = COND string( WHEN lv_alv_json IS NOT INITIAL THEN 'true' ELSE 'false' ).
    DATA(lv_trunc) = COND string( WHEN lv_truncated = abap_true THEN 'true' ELSE 'false' ).

    lv_json = |{ lv_o }"status":"success","report":"{ lv_report }","runtime_ms":{ lv_runtime },"alv_captured":{ lv_alv_captured }|.
    IF lv_alv_json IS NOT INITIAL.
      lv_json = |{ lv_json },"columns":{ lv_columns_json },"rows":{ lv_alv_json },"total_rows":{ lv_total_rows },"truncated":{ lv_trunc }|.
    ENDIF.
    lv_json = |{ lv_json }{ lv_c }|.

    rs_response = VALUE #( id = is_message-id success = abap_true data = lv_json ).
  ENDMETHOD.

  METHOD handle_get_text_elements.
    DATA: lt_textpool TYPE TABLE OF textpool,
          lv_program  TYPE progname.

    DATA(lv_prog_str) = extract_param( iv_params = is_message-params iv_name = 'program' ).
    DATA(lv_language) = extract_param( iv_params = is_message-params iv_name = 'language' ).

    IF lv_prog_str IS INITIAL.
      rs_response = build_error( iv_id = is_message-id iv_code = 'MISSING_PARAM' iv_message = 'Parameter program is required' ).
      RETURN.
    ENDIF.

    TRANSLATE lv_prog_str TO UPPER CASE.
    lv_program = lv_prog_str.

    DATA lv_lang TYPE sy-langu.
    IF lv_language IS NOT INITIAL.
      CALL FUNCTION 'CONVERSION_EXIT_ISOLA_INPUT'
        EXPORTING
          input            = lv_language
        IMPORTING
          output           = lv_lang
        EXCEPTIONS
          unknown_language = 1
          OTHERS           = 2.
      IF sy-subrc <> 0.
        rs_response = build_error(
          iv_id      = is_message-id
          iv_code    = 'INVALID_LANGUAGE'
          iv_message = |Unknown language code: { lv_language }|
        ).
        RETURN.
      ENDIF.
    ELSE.
      lv_lang = sy-langu.
    ENDIF.

    READ TEXTPOOL lv_program INTO lt_textpool LANGUAGE lv_lang.

    DATA(lv_o) = '{'.
    DATA(lv_c) = '}'.
    DATA lv_json TYPE string.
    lv_json = |{ lv_o }"program":"{ lv_program }","language":"{ lv_lang }","selection_texts":{ lv_o }|.

    DATA lv_first TYPE abap_bool VALUE abap_true.
    DATA lv_entry_str TYPE string.
    LOOP AT lt_textpool INTO DATA(ls_text) WHERE id = 'S'.
      IF lv_first = abap_false.
        lv_json = |{ lv_json },|.
      ENDIF.
      DATA(lv_key) = ls_text-key.
      CONDENSE lv_key.
      " Selection text entry has 8-char key prefix - strip it
      lv_entry_str = ls_text-entry.
      IF strlen( lv_entry_str ) > 8.
        lv_entry_str = lv_entry_str+8.
      ENDIF.
      lv_json = |{ lv_json }"{ lv_key }":"{ escape_json( lv_entry_str ) }"|.
      lv_first = abap_false.
    ENDLOOP.

    lv_json = |{ lv_json }{ lv_c },"text_symbols":{ lv_o }|.

    lv_first = abap_true.
    LOOP AT lt_textpool INTO ls_text WHERE id = 'I'.
      IF lv_first = abap_false.
        lv_json = |{ lv_json },|.
      ENDIF.
      lv_key = ls_text-key.
      CONDENSE lv_key.
      lv_entry_str = ls_text-entry.
      lv_json = |{ lv_json }"{ lv_key }":"{ escape_json( lv_entry_str ) }"|.
      lv_first = abap_false.
    ENDLOOP.

    lv_json = |{ lv_json }{ lv_c }{ lv_c }|.
    rs_response = VALUE #( id = is_message-id success = abap_true data = lv_json ).
  ENDMETHOD.

  METHOD handle_set_text_elements.
    DATA: lt_textpool TYPE TABLE OF textpool,
          lv_program  TYPE progname.

    DATA(lv_prog_str) = extract_param( iv_params = is_message-params iv_name = 'program' ).
    DATA(lv_language) = extract_param( iv_params = is_message-params iv_name = 'language' ).
    DATA(lv_sel_json) = extract_param_object( iv_params = is_message-params iv_name = 'selection_texts' ).
    DATA(lv_sym_json) = extract_param_object( iv_params = is_message-params iv_name = 'text_symbols' ).

    IF lv_prog_str IS INITIAL.
      rs_response = build_error( iv_id = is_message-id iv_code = 'MISSING_PARAM' iv_message = 'Parameter program is required' ).
      RETURN.
    ENDIF.

    TRANSLATE lv_prog_str TO UPPER CASE.
    lv_program = lv_prog_str.

    DATA lv_lang TYPE sy-langu.
    IF lv_language IS NOT INITIAL.
      CALL FUNCTION 'CONVERSION_EXIT_ISOLA_INPUT'
        EXPORTING
          input            = lv_language
        IMPORTING
          output           = lv_lang
        EXCEPTIONS
          unknown_language = 1
          OTHERS           = 2.
      IF sy-subrc <> 0.
        rs_response = build_error(
          iv_id      = is_message-id
          iv_code    = 'INVALID_LANGUAGE'
          iv_message = |Unknown language code: { lv_language }|
        ).
        RETURN.
      ENDIF.
    ELSE.
      lv_lang = sy-langu.
    ENDIF.

    READ TEXTPOOL lv_program INTO lt_textpool LANGUAGE lv_lang.

    DATA lv_sel_count TYPE i.
    DATA lv_sym_count TYPE i.

    IF lv_sel_json IS NOT INITIAL.
      DATA(lv_work) = lv_sel_json.
      WHILE lv_work CS '"'.
        DATA lv_key TYPE string.
        DATA lv_val TYPE string.
        FIND PCRE '"([^"]+)"\s*:\s*"([^"]*)"' IN lv_work SUBMATCHES lv_key lv_val.
        IF sy-subrc = 0.
          TRANSLATE lv_key TO UPPER CASE.
          REPLACE ALL OCCURRENCES OF '\"' IN lv_val WITH '"'.
          REPLACE ALL OCCURRENCES OF '\\' IN lv_val WITH '\'.

          DATA lv_textkey TYPE textpoolky.
          lv_textkey = lv_key.
          IF lv_val IS INITIAL.
            DELETE lt_textpool WHERE id = 'S' AND key = lv_textkey.
          ELSE.
            " Selection text entry must be: 8-char key prefix + text value
            DATA(lv_entry) = |{ lv_textkey WIDTH = 8 }{ lv_val }|.
            READ TABLE lt_textpool ASSIGNING FIELD-SYMBOL(<fs>) WITH KEY id = 'S' key = lv_textkey.
            IF sy-subrc = 0.
              <fs>-entry = lv_entry.
            ELSE.
              APPEND VALUE textpool( id = 'S' key = lv_textkey entry = lv_entry ) TO lt_textpool.
            ENDIF.
          ENDIF.
          lv_sel_count = lv_sel_count + 1.

          FIND FIRST OCCURRENCE OF |"{ lv_key }"| IN lv_work MATCH OFFSET DATA(lv_off) MATCH LENGTH DATA(lv_len) IGNORING CASE.
          IF sy-subrc = 0 AND strlen( lv_work ) > lv_off + lv_len.
            lv_work = lv_work+lv_off.
            lv_work = lv_work+lv_len.
          ELSE.
            EXIT.
          ENDIF.
        ELSE.
          EXIT.
        ENDIF.
      ENDWHILE.
    ENDIF.

    IF lv_sym_json IS NOT INITIAL.
      lv_work = lv_sym_json.
      WHILE lv_work CS '"'.
        CLEAR: lv_key, lv_val.
        FIND PCRE '"([^"]+)"\s*:\s*"([^"]*)"' IN lv_work SUBMATCHES lv_key lv_val.
        IF sy-subrc = 0.
          REPLACE ALL OCCURRENCES OF '\"' IN lv_val WITH '"'.
          REPLACE ALL OCCURRENCES OF '\\' IN lv_val WITH '\'.

          lv_textkey = lv_key.
          IF lv_val IS INITIAL.
            DELETE lt_textpool WHERE id = 'I' AND key = lv_textkey.
          ELSE.
            READ TABLE lt_textpool ASSIGNING <fs> WITH KEY id = 'I' key = lv_textkey.
            IF sy-subrc = 0.
              <fs>-entry = lv_val.
            ELSE.
              APPEND VALUE textpool( id = 'I' key = lv_textkey entry = lv_val ) TO lt_textpool.
            ENDIF.
          ENDIF.
          lv_sym_count = lv_sym_count + 1.

          FIND FIRST OCCURRENCE OF |"{ lv_key }"| IN lv_work MATCH OFFSET lv_off MATCH LENGTH lv_len.
          IF sy-subrc = 0 AND strlen( lv_work ) > lv_off + lv_len.
            lv_work = lv_work+lv_off.
            lv_work = lv_work+lv_len.
          ELSE.
            EXIT.
          ENDIF.
        ELSE.
          EXIT.
        ENDIF.
      ENDWHILE.
    ENDIF.

    INSERT TEXTPOOL lv_program FROM lt_textpool LANGUAGE lv_lang.

    DATA(lv_o) = '{'.
    DATA(lv_c) = '}'.
    DATA(lv_status) = COND string( WHEN sy-subrc = 0 THEN 'success' ELSE 'error' ).
    DATA lv_json TYPE string.
    lv_json = |{ lv_o }"status":"{ lv_status }","program":"{ lv_program }","language":"{ lv_lang }","selection_texts_set":{ lv_sel_count },"text_symbols_set":{ lv_sym_count }{ lv_c }|.

    rs_response = VALUE #( id = is_message-id success = abap_true data = lv_json ).
  ENDMETHOD.

  METHOD handle_get_variants.
    DATA: lt_varid   TYPE TABLE OF varid,
          lv_report  TYPE progname.

    DATA(lv_report_str) = extract_param( iv_params = is_message-params iv_name = 'report' ).

    IF lv_report_str IS INITIAL.
      rs_response = build_error( iv_id = is_message-id iv_code = 'MISSING_PARAM' iv_message = 'Parameter report is required' ).
      RETURN.
    ENDIF.

    TRANSLATE lv_report_str TO UPPER CASE.
    lv_report = lv_report_str.

    SELECT * FROM varid INTO TABLE lt_varid
      WHERE report = lv_report
      ORDER BY variant.

    DATA(lv_o) = '{'.
    DATA(lv_c) = '}'.
    DATA lv_json TYPE string.
    lv_json = |{ lv_o }"report":"{ lv_report }","variants":[|.

    DATA lv_first TYPE abap_bool VALUE abap_true.
    LOOP AT lt_varid INTO DATA(ls_var).
      IF lv_first = abap_false.
        lv_json = |{ lv_json },|.
      ENDIF.
      DATA(lv_protected) = COND string( WHEN ls_var-protected = abap_true THEN 'true' ELSE 'false' ).
      lv_json = |{ lv_json }{ lv_o }"name":"{ ls_var-variant }","protected":{ lv_protected }{ lv_c }|.
      lv_first = abap_false.
    ENDLOOP.

    lv_json = |{ lv_json }]{ lv_c }|.
    rs_response = VALUE #( id = is_message-id success = abap_true data = lv_json ).
  ENDMETHOD.

  METHOD resolve_class.
    DATA lv_class TYPE tadir-obj_name.

    ev_found = abap_false.
    lv_class = to_upper( iv_class ).

    SELECT SINGLE devclass, masterlang FROM tadir
      WHERE pgmid    = 'R3TR'
        AND object   = 'CLAS'
        AND obj_name = @lv_class
      INTO ( @ev_devclass, @ev_masterlang ).
    IF sy-subrc = 0.
      ev_found = abap_true.
    ENDIF.
  ENDMETHOD.

  METHOD resolve_language.
    ev_ok = abap_false.

    IF iv_language IS INITIAL.
      ev_lang = iv_default.
    ELSEIF strlen( iv_language ) = 1.
      " 1 character is already a SAP language key
      ev_lang = to_upper( iv_language ).
    ELSE.
      " 2 characters are ISO (ES -> S)
      DATA(lv_iso) = to_upper( iv_language ).
      CALL FUNCTION 'CONVERSION_EXIT_ISOLA_INPUT'
        EXPORTING
          input            = lv_iso
        IMPORTING
          output           = ev_lang
        EXCEPTIONS
          unknown_language = 1
          OTHERS           = 2.
      IF sy-subrc <> 0.
        RETURN.
      ENDIF.
    ENDIF.

    " The default (master language) is checked too: a blank language would
    " write a row with a blank key.
    SELECT SINGLE spras FROM t002 WHERE spras = @ev_lang INTO @DATA(lv_spras).
    ev_ok = xsdbool( sy-subrc = 0 ).
  ENDMETHOD.

  METHOD set_result.
    DATA(lv_data) = /ui2/cl_json=>serialize(
      data        = is_result
      pretty_name = /ui2/cl_json=>pretty_mode-low_case ).
    rs_response = VALUE #( id = iv_id success = abap_true data = lv_data ).
  ENDMETHOD.

  METHOD modify_param_text.
    DATA lt_parameters TYPE seos_parameters_w.
    DATA ls_parameter LIKE LINE OF lt_parameters.

    " SEO_PARAMETER_MODIFY rewrites the whole active SEOSUBCODF row, so it
    " needs the complete definition and not only the text.
    MOVE-CORRESPONDING is_co TO ls_parameter.
    MOVE-CORRESPONDING is_df TO ls_parameter.
    ls_parameter-langu    = iv_lang.
    ls_parameter-descript = iv_description.
    APPEND ls_parameter TO lt_parameters.

    CALL FUNCTION 'SEO_PARAMETER_MODIFY'
      EXPORTING
        parameters             = lt_parameters
      EXCEPTIONS
        component_not_existing = 1
        not_specified          = 2
        not_existing           = 3
        OTHERS                 = 4.
    IF sy-subrc <> 0.
      rv_error = |SEO_PARAMETER_MODIFY failed (sy-subrc { sy-subrc })|.
    ENDIF.

    " No COMMIT WORK / ROLLBACK WORK here: both end the push channel with
    " APC_ILLEGAL_STATEMENT. The framework commits when the handler returns.
  ENDMETHOD.

  METHOD handle_get_class_param_texts.
    DATA ls_req TYPE ty_get_request.
    DATA ls_response TYPE ty_get_response.
    DATA lr_method TYPE RANGE OF seosubco-cmpname.

    TRY.
        /ui2/cl_json=>deserialize( EXPORTING json = is_message-params CHANGING data = ls_req ).
      CATCH cx_root.
        rs_response = build_error( iv_id = is_message-id iv_code = 'INVALID_PARAM' iv_message = 'Parameters are not valid JSON' ).
        RETURN.
    ENDTRY.

    DATA(lv_class) = to_upper( ls_req-class_name ).
    DATA(lv_method) = to_upper( ls_req-method_name ).
    IF lv_class IS INITIAL.
      rs_response = build_error( iv_id = is_message-id iv_code = 'MISSING_PARAM' iv_message = 'Parameter class_name is required' ).
      RETURN.
    ENDIF.

    resolve_class(
      EXPORTING iv_class      = lv_class
      IMPORTING ev_found      = DATA(lv_found)
                ev_devclass   = DATA(lv_devclass)
                ev_masterlang = DATA(lv_masterlang) ).
    IF lv_found = abap_false.
      rs_response = build_error( iv_id = is_message-id iv_code = 'CLASS_NOT_FOUND' iv_message = |Class { lv_class } not found| ).
      RETURN.
    ENDIF.

    resolve_language(
      EXPORTING iv_language = ls_req-language
                iv_default  = lv_masterlang
      IMPORTING ev_lang     = DATA(lv_lang)
                ev_ok       = DATA(lv_lang_ok) ).
    IF lv_lang_ok = abap_false.
      rs_response = build_error( iv_id = is_message-id iv_code = 'INVALID_LANGUAGE' iv_message = |Unknown language code: { ls_req-language }| ).
      RETURN.
    ENDIF.

    DATA lv_class_key TYPE seosubco-clsname.
    lv_class_key = lv_class.
    IF lv_method IS NOT INITIAL.
      lr_method = VALUE #( ( sign = 'I' option = 'EQ' low = lv_method ) ).
    ENDIF.

    SELECT t_co~cmpname, t_co~sconame, t_co~cmptype, t_df~version, t_df~pardecltyp, t_tx~descript
      FROM seosubco AS t_co
      INNER JOIN seosubcodf AS t_df
        ON  t_df~clsname = t_co~clsname
        AND t_df~cmpname = t_co~cmpname
        AND t_df~sconame = t_co~sconame
      LEFT OUTER JOIN seosubcotx AS t_tx
        ON  t_tx~clsname = t_co~clsname
        AND t_tx~cmpname = t_co~cmpname
        AND t_tx~sconame = t_co~sconame
        AND t_tx~langu   = @lv_lang
      WHERE t_co~clsname = @lv_class_key
        AND t_co~scotype = '0'
        AND t_co~cmpname IN @lr_method
      ORDER BY t_co~cmpname, t_co~sconame, t_df~version
      INTO TABLE @DATA(lt_rows).

    ls_response-class_name      = lv_class.
    ls_response-package_name    = lv_devclass.
    ls_response-master_language = lv_masterlang.
    ls_response-language        = lv_lang.

    LOOP AT lt_rows ASSIGNING FIELD-SYMBOL(<ls_row>).
      ASSIGN ls_response-method_list[ method_name = <ls_row>-cmpname ] TO FIELD-SYMBOL(<ls_method>).
      IF sy-subrc <> 0.
        APPEND VALUE #( method_name = <ls_row>-cmpname cmp_type = <ls_row>-cmptype )
          TO ls_response-method_list ASSIGNING <ls_method>.
      ENDIF.

      DATA(lv_version) = COND string( WHEN <ls_row>-version = '1' THEN 'active' ELSE 'inactive' ).
      ASSIGN <ls_method>-params[ name = <ls_row>-sconame ] TO FIELD-SYMBOL(<ls_param>).
      IF sy-subrc = 0.
        <ls_param>-version = 'both'.
      ELSE.
        APPEND VALUE #(
          name        = <ls_row>-sconame
          decl_type   = <ls_row>-pardecltyp
          version     = lv_version
          description = <ls_row>-descript
          has_text    = xsdbool( <ls_row>-descript IS NOT INITIAL ) )
          TO <ls_method>-params.
      ENDIF.
    ENDLOOP.

    DATA(lv_data) = /ui2/cl_json=>serialize(
      data        = ls_response
      pretty_name = /ui2/cl_json=>pretty_mode-low_case ).
    rs_response = VALUE #( id = is_message-id success = abap_true data = lv_data ).
  ENDMETHOD.

  METHOD validate_set_request.
    CONSTANTS lc_max_length TYPE i VALUE 60.

    IF is_req-class_name IS INITIAL OR is_req-method_name IS INITIAL OR is_req-parameter_name IS INITIAL.
      rs_error = VALUE #( code = 'MISSING_PARAM' message = 'class_name, method_name and parameter_name are required' ).
    ELSEIF is_req-description IS INITIAL AND is_req-clear_text = abap_false.
      rs_error = VALUE #( code = 'CLEAR_REQUIRED' message = 'An empty description needs clear_text = true' ).
    ELSEIF is_req-description IS NOT INITIAL AND is_req-clear_text = abap_true.
      rs_error = VALUE #( code = 'INVALID_PARAM' message = 'clear_text = true cannot be combined with a description' ).
    ELSEIF strlen( is_req-description ) > lc_max_length.
      rs_error = VALUE #( code = 'TEXT_TOO_LONG'
                          message = |Description has { strlen( is_req-description ) } characters, the maximum is { lc_max_length }| ).
    ELSEIF contains( val = is_req-description pcre = '[[:cntrl:]]' ).
      rs_error = VALUE #( code = 'INVALID_PARAM' message = 'Description must not contain control characters (line breaks, tabs)' ).
    ENDIF.
  ENDMETHOD.

  METHOD locate_target.
    CONSTANTS lc_objtype_class TYPE c LENGTH 4 VALUE 'CLAS'.
    CONSTANTS lc_actvt_change  TYPE c LENGTH 2 VALUE '02'.
    DATA lv_cls TYPE seosubco-clsname.

    resolve_class(
      EXPORTING iv_class      = is_req-class_name
      IMPORTING ev_found      = DATA(lv_found)
                ev_devclass   = ev_devclass
                ev_masterlang = ev_masterlang ).
    IF lv_found = abap_false.
      es_error = VALUE #( code = 'CLASS_NOT_FOUND' message = |Class { is_req-class_name } not found| ).
      RETURN.
    ENDIF.

    resolve_language(
      EXPORTING iv_language = is_req-language
                iv_default  = ev_masterlang
      IMPORTING ev_lang     = ev_lang
                ev_ok       = DATA(lv_lang_ok) ).
    IF lv_lang_ok = abap_false.
      es_error = VALUE #( code = 'INVALID_LANGUAGE' message = |Unknown language code: { is_req-language }| ).
      RETURN.
    ENDIF.

    lv_cls = is_req-class_name.
    AUTHORITY-CHECK OBJECT 'S_DEVELOP'
      ID 'DEVCLASS' FIELD ev_devclass
      ID 'OBJTYPE'  FIELD lc_objtype_class
      ID 'OBJNAME'  FIELD lv_cls
      ID 'P_GROUP'  DUMMY
      ID 'ACTVT'    FIELD lc_actvt_change.
    IF sy-subrc <> 0.
      es_error = VALUE #( code = 'NO_AUTHORIZATION' message = |No change authorization (S_DEVELOP) for class { is_req-class_name }| ).
    ENDIF.
  ENDMETHOD.

  METHOD validate_transport.
    CONSTANTS lc_local_package_prefix TYPE c LENGTH 1 VALUE '$'.
    DATA ls_header TYPE e070.

    IF iv_devclass(1) = lc_local_package_prefix.
      IF is_req-transport IS NOT INITIAL.
        rs_error = VALUE #( code = 'TRANSPORT_NOT_ALLOWED'
                            message = |Class { is_req-class_name } is in local package { iv_devclass }; no transport request is used| ).
      ENDIF.
      RETURN.
    ENDIF.

    rs_error = check_transport_given( is_req = is_req iv_devclass = iv_devclass ).
    IF rs_error IS NOT INITIAL.
      RETURN.
    ENDIF.

    DATA(lv_request) = CONV trkorr( is_req-transport ).
    read_request_header( EXPORTING iv_request = lv_request
                         IMPORTING es_header  = ls_header
                                   es_error   = rs_error ).
    IF rs_error IS NOT INITIAL.
      RETURN.
    ENDIF.

    rs_error = check_request_ownership( iv_request = lv_request is_header = ls_header ).
    IF rs_error IS NOT INITIAL.
      RETURN.
    ENDIF.

    rs_error = check_class_lock( iv_request = lv_request iv_class = is_req-class_name ).
    IF rs_error IS NOT INITIAL.
      RETURN.
    ENDIF.

    rs_error = check_class_original( is_req-class_name ).
  ENDMETHOD.

  METHOD check_transport_given.
    IF is_req-transport IS INITIAL.
      rs_error = VALUE #( code = 'TRANSPORT_REQUIRED'
                          message = |Class { is_req-class_name } is in transportable package { iv_devclass }; a workbench request must be given| ).
      RETURN.
    ENDIF.
    IF NOT matches( val = is_req-transport pcre = '[A-Z0-9]{3}K[0-9]{6}' ).
      rs_error = VALUE #( code = 'TRANSPORT_INVALID' message = |{ is_req-transport } is not a transport request number| ).
    ENDIF.
  ENDMETHOD.

  METHOD read_request_header.
    CLEAR: es_header, es_error.
    SELECT SINGLE trfunction, trstatus, strkorr, as4user FROM e070
      WHERE trkorr = @iv_request
      INTO CORRESPONDING FIELDS OF @es_header.
    IF sy-subrc <> 0.
      es_error = VALUE #( code = 'TRANSPORT_NOT_FOUND' message = |Transport request { iv_request } does not exist| ).
      RETURN.
    ENDIF.
    IF es_header-strkorr IS NOT INITIAL.
      es_error = VALUE #( code = 'TRANSPORT_IS_TASK'
                          message = |{ iv_request } is a task; give its request { es_header-strkorr }| ).
      RETURN.
    ENDIF.
    IF es_header-trfunction <> gc_trfunction_workbench.
      es_error = VALUE #( code = 'TRANSPORT_INVALID' message = |{ iv_request } is not a workbench request| ).
      RETURN.
    ENDIF.
    IF es_header-trstatus <> gc_trstatus_modifiable.
      es_error = VALUE #( code = 'TRANSPORT_NOT_MODIFIABLE' message = |Transport request { iv_request } is not modifiable| ).
    ENDIF.
  ENDMETHOD.

  METHOD check_request_ownership.
    " The request has to be the user's own, or hold a modifiable task of theirs
    IF is_header-as4user = sy-uname.
      RETURN.
    ENDIF.
    IF has_own_modifiable_task( iv_request ) = abap_false.
      rs_error = VALUE #( code = 'TRANSPORT_NOT_OWNED'
                          message = |Transport request { iv_request } belongs to { is_header-as4user } and you have no task in it| ).
    ENDIF.
  ENDMETHOD.

  METHOD has_own_modifiable_task.
    SELECT SINGLE trkorr FROM e070
      WHERE strkorr = @iv_request AND as4user = @sy-uname AND trstatus = @gc_trstatus_modifiable
      INTO @DATA(lv_own_task).
    rv_has_task = xsdbool( sy-subrc = 0 ).
  ENDMETHOD.

  METHOD check_class_lock.
    " TLOCK keeps every lock of a class (whole class, CLSD, CPUB, METH...) under
    " OBJECT = CLAS, LOKEY = name padded to 30 characters + a suffix
    DATA(lv_lock_pattern) = replace( val = iv_class sub = '_' with = '#_' occ = 0 ) && '%'.
    SELECT trkorr, author, lokey FROM tlock
      WHERE object = 'CLAS' AND lokey LIKE @lv_lock_pattern ESCAPE '#'
      INTO TABLE @DATA(lt_locks).
    LOOP AT lt_locks INTO DATA(ls_lock).
      IF ls_lock-lokey(30) <> iv_class.
        CONTINUE.
      ENDIF.
      DATA(lv_lock_request) = request_of_lock( ls_lock-trkorr ).
      IF lv_lock_request <> iv_request.
        rs_error = VALUE #( code = 'OBJECT_LOCKED_IN_OTHER_REQUEST'
                            message = |Class { iv_class } is already in request { lv_lock_request } (user { ls_lock-author })| ).
        RETURN.
      ENDIF.
    ENDLOOP.
  ENDMETHOD.

  METHOD request_of_lock.
    DATA lv_parent TYPE e070-strkorr.
    SELECT SINGLE strkorr FROM e070 WHERE trkorr = @iv_trkorr INTO @lv_parent.
    rv_request = COND trkorr( WHEN lv_parent IS INITIAL THEN iv_trkorr ELSE lv_parent ).
  ENDMETHOD.

  METHOD check_class_original.
    CONSTANTS lc_objtype_class TYPE tadir-object VALUE 'CLAS'.
    DATA lv_class TYPE tadir-obj_name.

    lv_class = iv_class.
    SELECT SINGLE srcsystem FROM tadir
      WHERE pgmid = 'R3TR' AND object = @lc_objtype_class AND obj_name = @lv_class
      INTO @DATA(lv_srcsystem).
    IF sy-subrc = 0 AND lv_srcsystem <> sy-sysid.
      rs_error = VALUE #( code = 'ORIGINAL_IN_OTHER_SYSTEM'
                          message = |Class { iv_class } has its original in system { lv_srcsystem }| ).
    ENDIF.
  ENDMETHOD.

  METHOD record_in_request.
    CONSTANTS lc_pgmid_limu        TYPE c LENGTH 4 VALUE 'LIMU'.
    CONSTANTS lc_object_class_def  TYPE c LENGTH 4 VALUE 'CLSD'.
    CONSTANTS lc_object_public     TYPE c LENGTH 4 VALUE 'CPUB'.
    DATA lv_text TYPE string.

    " Same entries SE24 records for a change of a parameter text. The function
    " never opens a dialog and needs the request: it cannot create one.
    DATA(lt_objects) = VALUE tredt_objects(
      ( pgmid = lc_pgmid_limu object = lc_object_class_def obj_name = iv_class )
      ( pgmid = lc_pgmid_limu object = lc_object_public    obj_name = iv_class ) ).

    CALL FUNCTION 'TR_RECORD_OBJ_CHANGE_TO_REQ'
      EXPORTING
        iv_request      = iv_request
        it_objects      = lt_objects
      IMPORTING
        ev_no_recording = ev_no_recording
      EXCEPTIONS
        cancel          = 1
        error_message   = 2
        OTHERS          = 3.
    IF sy-subrc <> 0.
      IF sy-msgid IS NOT INITIAL.
        MESSAGE ID sy-msgid TYPE 'E' NUMBER sy-msgno WITH sy-msgv1 sy-msgv2 sy-msgv3 sy-msgv4 INTO lv_text.
      ELSE.
        lv_text = |function ended with return code { sy-subrc } and no message|.
      ENDIF.
      ev_error = |Could not record the class in request { iv_request }: { lv_text }|.
    ENDIF.
  ENDMETHOD.

  METHOD apply_set_request.
    " Runs inside the class lock: everything read here is what gets rewritten.
    DATA ls_co         TYPE seosubco.
    DATA ls_df         TYPE seosubcodf.
    DATA ls_error      TYPE ty_error.
    DATA lv_old        TYPE seosubcotx-descript.
    DATA lv_old_exists TYPE abap_bool.

    DATA(ls_key) = build_param_key( is_req ).
    read_param_rows( EXPORTING is_key   = ls_key
                               is_req   = is_req
                     IMPORTING es_co    = ls_co
                               es_df    = ls_df
                               es_error = ls_error ).
    IF ls_error IS NOT INITIAL.
      rs_response = build_error( iv_id = iv_id iv_code = ls_error-code iv_message = ls_error-message ).
      RETURN.
    ENDIF.

    read_param_text( EXPORTING is_key    = ls_key
                               iv_lang   = iv_lang
                     IMPORTING ev_text   = lv_old
                               ev_exists = lv_old_exists ).

    DATA(ls_result) = VALUE ty_set_response(
      class_name      = is_req-class_name
      method_name     = is_req-method_name
      parameter_name  = is_req-parameter_name
      language        = iv_lang
      master_language = iv_masterlang
      old_description = lv_old
      new_description = is_req-description ).

    IF ( lv_old_exists = abap_true AND lv_old = CONV seodescr( is_req-description ) )
       OR ( lv_old_exists = abap_false AND is_req-description IS INITIAL ).
      ls_result-status    = 'unchanged'.
      ls_result-row_state = row_state( iv_exists = lv_old_exists iv_text = lv_old ).
      ls_result-verified  = abap_true.
      rs_response = set_result( iv_id = iv_id is_result = ls_result ).
      RETURN.
    ENDIF.

    record_change( EXPORTING is_req    = is_req
                   IMPORTING es_error  = ls_error
                   CHANGING  cs_result = ls_result ).
    IF ls_error IS NOT INITIAL.
      rs_response = build_error( iv_id = iv_id iv_code = ls_error-code iv_message = ls_error-message ).
      RETURN.
    ENDIF.

    DATA(lv_error) = modify_param_text(
      is_co          = ls_co
      is_df          = ls_df
      iv_lang        = iv_lang
      iv_description = is_req-description ).
    IF lv_error IS NOT INITIAL.
      rs_response = build_error( iv_id = iv_id iv_code = 'MODIFY_FAILED'
                                iv_message = |{ lv_error } (state may be partial: check with param_texts)| ).
      RETURN.
    ENDIF.

    verify_written_text( EXPORTING is_key    = ls_key
                                   is_req    = is_req
                                   iv_lang   = iv_lang
                         IMPORTING es_error  = ls_error
                         CHANGING  cs_result = ls_result ).
    IF ls_error IS NOT INITIAL.
      rs_response = build_error( iv_id = iv_id iv_code = ls_error-code iv_message = ls_error-message ).
      RETURN.
    ENDIF.

    rs_response = set_result( iv_id = iv_id is_result = ls_result ).
  ENDMETHOD.

  METHOD build_param_key.
    rs_key-clsname = is_req-class_name.
    rs_key-cmpname = is_req-method_name.
    rs_key-sconame = is_req-parameter_name.
  ENDMETHOD.

  METHOD read_param_rows.
    CLEAR: es_co, es_df, es_error.
    SELECT SINGLE * FROM seosubco
      WHERE clsname = @is_key-clsname AND cmpname = @is_key-cmpname AND sconame = @is_key-sconame
      INTO @es_co.
    IF sy-subrc <> 0.
      es_error = describe_missing_param( is_key = is_key is_req = is_req ).
      RETURN.
    ENDIF.
    IF es_co-scotype <> gc_scotype_param.
      es_error = VALUE #( code = 'PARAM_NOT_FOUND' message = |{ is_req-parameter_name } is not a parameter (it is an exception)| ).
      RETURN.
    ENDIF.

    SELECT SINGLE * FROM seosubcodf
      WHERE clsname = @is_key-clsname AND cmpname = @is_key-cmpname AND sconame = @is_key-sconame
        AND version = @gc_version_active
      INTO @es_df.
    IF sy-subrc <> 0.
      es_error = VALUE #( code = 'PARAM_INACTIVE_ONLY' message = |Parameter { is_req-parameter_name } has no active version; activate the class first| ).
      RETURN.
    ENDIF.

    " With a pending inactive version it is not verified whether activating it
    " keeps the text, so refuse instead of risking a lost change.
    SELECT SINGLE version FROM seosubcodf
      WHERE clsname = @is_key-clsname AND cmpname = @is_key-cmpname AND sconame = @is_key-sconame
        AND version = @gc_version_inactive
      INTO @DATA(lv_inactive).
    IF sy-subrc = 0.
      es_error = VALUE #( code = 'CLASS_HAS_INACTIVE_VERSION'
                          message = |Parameter { is_req-parameter_name } has a pending inactive version; activate the class first| ).
    ENDIF.
  ENDMETHOD.

  METHOD describe_missing_param.
    SELECT SINGLE cmpname FROM seocompo
      WHERE clsname = @is_key-clsname AND cmpname = @is_key-cmpname
      INTO @DATA(lv_cmp_found).
    IF sy-subrc = 0.
      rs_error = VALUE #( code = 'PARAM_NOT_FOUND' message = |Parameter { is_req-parameter_name } not found in method { is_req-method_name }| ).
    ELSEIF is_req-method_name CS '~'.
      rs_error = VALUE #( code = 'METHOD_NOT_FOUND' message = |Method { is_req-method_name } is inherited from an interface; change the text in the interface| ).
    ELSE.
      rs_error = VALUE #( code = 'METHOD_NOT_FOUND' message = |Method { is_req-method_name } not found in class { is_req-class_name }| ).
    ENDIF.
  ENDMETHOD.

  METHOD read_param_text.
    CLEAR: ev_text, ev_exists.
    SELECT SINGLE descript FROM seosubcotx
      WHERE clsname = @is_key-clsname AND cmpname = @is_key-cmpname AND sconame = @is_key-sconame
        AND langu = @iv_lang
      INTO @ev_text.
    ev_exists = xsdbool( sy-subrc = 0 ).
  ENDMETHOD.

  METHOD row_state.
    rv_state = COND #( WHEN iv_exists = abap_false THEN 'absent'
                       WHEN iv_text IS INITIAL THEN 'blank'
                       ELSE 'present' ).
  ENDMETHOD.

  METHOD record_change.
    CLEAR es_error.
    IF is_req-transport IS INITIAL.
      RETURN.
    ENDIF.
    record_in_request(
      EXPORTING iv_request      = CONV trkorr( is_req-transport )
                iv_class        = is_req-class_name
      IMPORTING ev_no_recording = DATA(lv_no_recording)
                ev_error        = DATA(lv_record_error) ).
    IF lv_record_error IS NOT INITIAL.
      es_error = VALUE #( code = 'TRANSPORT_RECORD_FAILED' message = lv_record_error ).
      RETURN.
    ENDIF.
    cs_result-transport = is_req-transport.
    cs_result-recording = COND #( WHEN lv_no_recording = abap_true THEN 'not_needed' ELSE 'recorded' ).
  ENDMETHOD.

  METHOD verify_written_text.
    CLEAR es_error.
    read_param_text( EXPORTING is_key    = is_key
                               iv_lang   = iv_lang
                     IMPORTING ev_text   = DATA(lv_new)
                               ev_exists = DATA(lv_exists_after) ).
    cs_result-status    = 'changed'.
    cs_result-row_state = row_state( iv_exists = lv_exists_after iv_text = lv_new ).
    cs_result-verified  = xsdbool( ( is_req-description IS INITIAL AND lv_new IS INITIAL )
                                   OR ( is_req-description IS NOT INITIAL AND lv_new = CONV seodescr( is_req-description ) ) ).
    IF cs_result-verified = abap_false.
      es_error = VALUE #( code = 'VERIFY_FAILED' message = |The change may have been saved; the table shows '{ lv_new }'. Check with param_texts| ).
    ENDIF.
  ENDMETHOD.

  METHOD handle_set_class_param_text.
    DATA ls_req TYPE ty_set_request.
    DATA lv_lock_name TYPE seoclsenq-clsname.

    TRY.
        /ui2/cl_json=>deserialize( EXPORTING json = is_message-params CHANGING data = ls_req ).
      CATCH cx_root.
        rs_response = build_error( iv_id = is_message-id iv_code = 'INVALID_PARAM' iv_message = 'Parameters are not valid JSON' ).
        RETURN.
    ENDTRY.

    ls_req-class_name     = to_upper( ls_req-class_name ).
    ls_req-method_name    = to_upper( ls_req-method_name ).
    ls_req-parameter_name = to_upper( ls_req-parameter_name ).
    ls_req-transport      = to_upper( condense( ls_req-transport ) ).
    " Trailing blanks do not survive CHAR60, so compare and store the trimmed text
    ls_req-description    = shift_right( val = shift_left( val = ls_req-description sub = ` ` ) sub = ` ` ).

    DATA(ls_error) = validate_set_request( ls_req ).
    IF ls_error IS INITIAL.
      locate_target(
        EXPORTING is_req        = ls_req
        IMPORTING es_error      = ls_error
                  ev_lang       = DATA(lv_lang)
                  ev_masterlang = DATA(lv_masterlang)
                  ev_devclass   = DATA(lv_devclass) ).
    ENDIF.
    IF ls_error IS INITIAL.
      ls_error = validate_transport( is_req = ls_req iv_devclass = lv_devclass ).
    ENDIF.
    IF ls_error IS NOT INITIAL.
      rs_response = build_error( iv_id = is_message-id iv_code = ls_error-code iv_message = ls_error-message ).
      RETURN.
    ENDIF.

    " _scope 1 keeps the lock out of the update task, so the DEQUEUE below is
    " what releases it; the WebSocket session is long-lived and would hold it
    " otherwise. Locking comes before any read: what is read is what is rewritten.
    " SE24 and ADT lock the class under its class-pool name (padded with '='
    " to 30 characters). A name padded with blanks is a different lock
    " argument and would never collide with theirs.
    lv_lock_name = ls_req-class_name.
    TRANSLATE lv_lock_name USING ' ='.
    CALL FUNCTION 'ENQUEUE_ESEOCLASS'
      EXPORTING
        mode_seoclsenq = 'E'
        clsname        = lv_lock_name
        _scope         = '1'
      EXCEPTIONS
        foreign_lock   = 1
        system_failure = 2
        OTHERS         = 3.
    CASE sy-subrc.
      WHEN 0.
      WHEN 1.
        rs_response = build_error( iv_id = is_message-id iv_code = 'FOREIGN_LOCK' iv_message = |Class { ls_req-class_name } is locked by user { sy-msgv1 }| ).
        RETURN.
      WHEN OTHERS.
        rs_response = build_error( iv_id = is_message-id iv_code = 'LOCK_FAILED' iv_message = |Could not lock class { ls_req-class_name }| ).
        RETURN.
    ENDCASE.

    TRY.
        rs_response = apply_set_request(
          iv_id         = is_message-id
          is_req        = ls_req
          iv_lang       = lv_lang
          iv_masterlang = lv_masterlang ).
      CATCH cx_root INTO DATA(lx_error).
        rs_response = build_error( iv_id = is_message-id iv_code = 'MODIFY_FAILED' iv_message = |{ lx_error->get_text( ) } (state may be partial: check with param_texts)| ).
    ENDTRY.

    CALL FUNCTION 'DEQUEUE_ESEOCLASS'
      EXPORTING
        mode_seoclsenq = 'E'
        clsname        = lv_lock_name.
  ENDMETHOD.

  METHOD extract_param.
    DATA lv_name TYPE string.
    lv_name = iv_name.
    CONDENSE lv_name.

    DATA lv_search TYPE string.
    lv_search = |"{ lv_name }":|.
    DATA lv_pos TYPE i.
    FIND lv_search IN iv_params MATCH OFFSET lv_pos.
    IF sy-subrc = 0.
      DATA lv_rest TYPE string.
      lv_rest = iv_params+lv_pos.
      FIND PCRE ':\s*"([^"]*)"' IN lv_rest SUBMATCHES rv_value.
    ENDIF.
  ENDMETHOD.

  METHOD extract_param_object.
    DATA lv_name TYPE string.
    lv_name = iv_name.
    CONDENSE lv_name.

    DATA(lv_search) = |"{ lv_name }":|.
    DATA lv_pos TYPE i.
    FIND lv_search IN iv_params MATCH OFFSET lv_pos.
    IF sy-subrc <> 0.
      RETURN.
    ENDIF.

    DATA(lv_rest) = iv_params+lv_pos.
    DATA(lv_brace) = find( val = lv_rest sub = '{' ).
    IF lv_brace < 0.
      RETURN.
    ENDIF.

    DATA lv_depth TYPE i.
    DATA lv_i TYPE i.
    lv_i = lv_brace.
    DATA(lv_len) = strlen( lv_rest ).
    WHILE lv_i < lv_len.
      DATA(lv_char) = lv_rest+lv_i(1).
      IF lv_char = '{'.
        lv_depth = lv_depth + 1.
      ELSEIF lv_char = '}'.
        lv_depth = lv_depth - 1.
        IF lv_depth = 0.
          DATA(lv_obj_len) = lv_i - lv_brace + 1.
          rv_json = lv_rest+lv_brace(lv_obj_len).
          RETURN.
        ENDIF.
      ENDIF.
      lv_i = lv_i + 1.
    ENDWHILE.
  ENDMETHOD.

  METHOD escape_json.
    rv_escaped = iv_string.
    REPLACE ALL OCCURRENCES OF '\' IN rv_escaped WITH '\\'.
    REPLACE ALL OCCURRENCES OF '"' IN rv_escaped WITH '\"'.
    REPLACE ALL OCCURRENCES OF cl_abap_char_utilities=>cr_lf IN rv_escaped WITH '\n'.
    REPLACE ALL OCCURRENCES OF cl_abap_char_utilities=>newline IN rv_escaped WITH '\n'.
    REPLACE ALL OCCURRENCES OF cl_abap_char_utilities=>horizontal_tab IN rv_escaped WITH '\t'.
  ENDMETHOD.

  METHOD build_error.
    DATA(lv_o) = '{'.
    DATA(lv_c) = '}'.
    rs_response = VALUE #(
      id      = iv_id
      success = abap_false
      error   = |{ lv_o }"code":"{ iv_code }","message":"{ escape_json( iv_message ) }"{ lv_c }|
    ).
  ENDMETHOD.

ENDCLASS.
